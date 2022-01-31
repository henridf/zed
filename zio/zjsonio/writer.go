package zjsonio

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/brimdata/zed"
	astzed "github.com/brimdata/zed/compiler/ast/zed"
)

type Object struct {
	Schema string        `json:"schema"`
	Types  []astzed.Type `json:"types,omitempty"`
	Values []interface{} `json:"values"`
}

func unmarshal(b []byte) (*Object, error) {
	var template struct {
		Schema string        `json:"schema"`
		Types  []interface{} `json:"types,omitempty"`
		Values []interface{} `json:"values"`
	}
	if err := json.Unmarshal(b, &template); err != nil {
		return nil, err
	}
	var types []astzed.Type
	for _, t := range template.Types {
		// We should enhance the unpacker to take the template struct
		// here so we don't have to call UnmarshalObject.  But not
		// a big deal because we only do it for inbound zjson (which is
		// not performance critical and only for typedefs which are
		// typically infrequent.)  See issue #2702.
		object, err := unpacker.UnmarshalObject(t)
		if object == nil || err != nil {
			return nil, err
		}
		typ, ok := object.(astzed.Type)
		if !ok {
			return nil, fmt.Errorf("ZJSON types object is not a type: %s", string(b))
		}
		types = append(types, typ)
	}
	return &Object{
		Schema: template.Schema,
		Types:  types,
		Values: template.Values,
	}, nil
}

type Writer struct {
	writer io.WriteCloser
	stream *Stream
}

func NewWriter(w io.WriteCloser) *Writer {
	return &Writer{
		writer: w,
		stream: NewStream(),
	}
}

func (w *Writer) Close() error {
	return w.writer.Close()
}

func (w *Writer) Write(r *zed.Value) error {
	rec, err := w.stream.Transform(r)
	if err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = w.writer.Write(b)
	if err != nil {
		return err
	}
	return w.write("\n")
}

func (w *Writer) write(s string) error {
	_, err := w.writer.Write([]byte(s))
	return err
}
