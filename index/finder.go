package index

import (
	"context"
	"errors"
	"fmt"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/order"
	"github.com/brimdata/zed/pkg/field"
	"github.com/brimdata/zed/pkg/storage"
	"github.com/brimdata/zed/runtime/expr"
	"github.com/brimdata/zed/zio"
	"github.com/brimdata/zed/zio/zngio"
	"github.com/brimdata/zed/zson"
)

type Operator string

const (
	EQL Operator = "="
	GT  Operator = ">"
	GTE Operator = ">="
	LT  Operator = "<"
	LTE Operator = "<="
)

var ErrNotFound = errors.New("key not found")

// Finder looks up values in a microindex using its embedded index.
type Finder struct {
	*Reader
	zctx *zed.Context
	uri  *storage.URI
}

type KeyValue struct {
	Key   field.Path
	Value zed.Value
}

// NewFinder returns an object that is used to lookup keys in a microindex.
// It opens the file and reads the trailer, returning errors if the file is
// corrupt, doesn't exist, or has an invalid trailer.  If the microindex exists
// but is empty, zero values are returned for any lookups. If the microindex
// does not exist, a wrapped zqe.NotFound error is returned.
func NewFinder(ctx context.Context, zctx *zed.Context, engine storage.Engine, uri *storage.URI) (*Finder, error) {
	reader, err := NewReaderFromURI(ctx, zctx, engine, uri)
	if err != nil {
		return nil, err
	}
	return &Finder{
		Reader: reader,
		zctx:   zctx,
		uri:    uri,
	}, nil
}

type keyCompareFn func(expr.Context, *zed.Value) int

// lookup searches for a match of the given key compared to the
// key values in the records read from the reader.  If the op argument is eql
// then only exact matches are returned.  Otherwise, the record with the
// largest key smaller (or larger) than the key argument is returned.
func lookup(reader zio.Reader, compare keyCompareFn, o order.Which, op Operator) (*zed.Value, error) {
	if o == order.Asc {
		return lookupAsc(reader, compare, op)
	}
	return lookupDesc(reader, compare, op)
}

func lookupAsc(reader zio.Reader, fn keyCompareFn, op Operator) (*zed.Value, error) {
	var prev *zed.Value
	ectx := expr.NewContext()
	for {
		rec, err := reader.Read()
		if rec == nil || err != nil {
			if op == EQL || op == GTE || op == GT {
				prev = nil
			}
			return prev, err
		}
		if cmp := fn(ectx, rec); cmp >= 0 {
			if cmp == 0 && op.hasEqual() {
				return rec.Copy(), nil
			}
			if op == LTE || op == LT {
				return prev, nil
			}
			if op == EQL {
				return nil, nil
			}
			if !(op == GT && cmp == 0) {
				return rec.Copy(), nil
			}
		}
		prev = rec.Copy()
	}
}

func lookupDesc(reader zio.Reader, fn keyCompareFn, op Operator) (*zed.Value, error) {
	ectx := expr.NewContext()
	var prev *zed.Value
	for {
		rec, err := reader.Read()
		if rec == nil || err != nil {
			if op == EQL || op == LTE || op == LT {
				prev = nil
			}
			return prev, err
		}
		if cmp := fn(ectx, rec); cmp <= 0 {
			if cmp == 0 && op.hasEqual() {
				return rec.Copy(), nil
			}
			if op == GTE || op == GT {
				return prev, nil
			}
			if op == EQL {
				return nil, nil
			}
			if !(op == LT && cmp == 0) {
				return rec.Copy(), nil
			}
		}
		prev = rec.Copy()
	}
}

func (f *Finder) search(compare keyCompareFn) (*zngio.Reader, error) {
	if f.reader == nil {
		panic("finder hasn't been opened")
	}
	// We start with the topmost level of the microindex file and
	// find the first key that matches according to the comparison,
	// then repeat the process for that frame in the next index file
	// till we get to the base layer and return a reader positioned at
	// that offset.
	n := len(f.sections)
	off := int64(0)
	for level := 1; level < n; level++ {
		reader, err := f.newSectionReader(level, off)
		if err != nil {
			return nil, err
		}
		op := LTE
		if f.meta.Order == order.Desc {
			op = GTE
		}
		rec, err := lookup(reader, compare, f.meta.Order, op)
		reader.Close()
		if err != nil {
			return nil, err
		}
		if rec == nil {
			// This key can't be in the microindex since it is
			// smaller than the smallest key present.
			return nil, ErrNotFound
		}
		child := rec.Deref(f.meta.ChildOffsetField)
		if child == nil {
			return nil, fmt.Errorf("B-tree child field is missing")
		}
		off = child.AsInt()
	}
	return f.newSectionReader(0, off)
}

func (f *Finder) Lookup(kvs ...KeyValue) (*zed.Value, error) {
	return f.Nearest("=", kvs...)
}

func (f *Finder) LookupAll(ctx context.Context, hits chan<- *zed.Value, kvs []KeyValue) error {
	if f.IsEmpty() {
		return nil
	}
	compare := compareFn(f.zctx, kvs)
	reader, err := f.search(compare)
	if err != nil {
		return err
	}
	defer reader.Close()
	for {
		// As long as we have an exact key-match, where unset key
		// columns are "don't care", keep reading records and return
		// them via the channel.
		rec, err := lookup(reader, compare, f.meta.Order, EQL)
		if err != nil {
			return err
		}
		if rec == nil {
			return nil
		}
		select {
		case hits <- rec:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func compareFn(zctx *zed.Context, kvs []KeyValue) keyCompareFn {
	accessors := make([]expr.Evaluator, len(kvs))
	values := make([]zed.Value, len(kvs))
	for i := range kvs {
		accessors[i] = expr.NewDottedExpr(zctx, kvs[i].Key)
		values[i] = kvs[i].Value
	}
	fn := expr.NewValueCompareFn(false)
	return func(ectx expr.Context, this *zed.Value) int {
		for i := range kvs {
			val := accessors[i].Eval(ectx, this)
			if c := fn(val, &values[i]); c != 0 {
				return c
			}
		}
		return 0
	}
}

// Nearest finds the zed.Value in the index that is nearest to kvs according to
// operator.
func (f *Finder) Nearest(operator string, kvs ...KeyValue) (*zed.Value, error) {
	op := Operator(operator)
	if !op.valid() {
		return nil, fmt.Errorf("unsupported operator: %s", operator)
	}
	if f.IsEmpty() {
		return nil, nil
	}
	compare := compareFn(f.zctx, kvs)
	reader, err := f.search(compare)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return lookup(reader, compare, f.meta.Order, op)
}

// ParseKeys uses the key template from the microindex trailer to parse
// a slice of string values which correspnod to the DFS-order
// of the fields in the key.  The inputs may be smaller than the
// number of key fields, in which case they are "don't cares"
// in terms of key lookups.  Any don't-care fields must all be
// at the end of the key record.
func (f *Finder) ParseKeys(inputs ...string) ([]KeyValue, error) {
	if f.IsEmpty() {
		return nil, nil
	}
	keys := f.meta.Keys
	if len(inputs) > len(keys) {
		return nil, fmt.Errorf("too many keys: expected at most %d but got %d", len(keys), len(inputs))
	}
	kvs := make([]KeyValue, 0, len(inputs))
	for k := range inputs {
		if k < len(inputs) {
			s := inputs[k]
			zv, err := zson.ParseValue(f.zctx, s)
			if err != nil {
				return nil, fmt.Errorf("could not parse %q: %w", s, err)
			}
			kvs = append(kvs, KeyValue{Key: keys[k], Value: *zv})
		}
	}
	return kvs, nil
}

func (o Operator) hasEqual() bool {
	switch o {
	case EQL, GTE, LTE:
		return true
	}
	return false
}

func (o Operator) valid() bool {
	switch o {
	case EQL, GT, GTE, LT, LTE:
		return true
	}
	return false
}
