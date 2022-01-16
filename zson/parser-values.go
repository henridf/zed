package zson

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/brimdata/zed"
	astzed "github.com/brimdata/zed/compiler/ast/zed"
	"github.com/brimdata/zed/pkg/nano"
	"github.com/brimdata/zed/zcode"
)

func (p *Parser) ParseValue() (astzed.Value, error) {
	v, err := p.matchValue()
	if err == io.EOF {
		err = nil
	}
	if v == nil && err == nil {
		if err := p.lexer.check(1); (err != nil && err != io.EOF) || len(p.lexer.cursor) > 0 {
			return nil, errors.New("zson syntax error")
		}
	}
	return v, err
}

func noEOF(err error) error {
	if err == io.EOF {
		err = nil
	}
	return err
}

func (p *Parser) matchValue() (astzed.Value, error) {
	if val, err := p.matchRecord(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	if val, err := p.matchArray(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	if val, err := p.matchSetOrMap(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	if val, err := p.matchTypeValue(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	// Primitive comes last as the other matchers short-circuit more
	// efficiently on sentinel characters.
	if val, err := p.matchPrimitive(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	// But enum really goes last becase we don't want it to pick up
	// true, false, or null.
	if val, err := p.matchEnum(); val != nil || err != nil {
		return p.decorate(val, err)
	}
	return nil, nil
}

func anyAsValue(any astzed.Any) *astzed.ImpliedValue {
	return &astzed.ImpliedValue{
		Kind: "ImpliedValue",
		Of:   any,
	}
}

func (p *Parser) decorate(any astzed.Any, err error) (astzed.Value, error) {
	if err != nil {
		return nil, err
	}
	// First see if there's a short-form typedef decorator.
	// If there isn't, matchShortForm() returns the AnyValue wrapped
	// in an astzed.ImpliedValue (as astzed.Value)  Otherwise, it returns an
	//  astzed.DefVal (as astzed.Vaue).
	val, ok, err := p.matchDecorator(any, nil)
	if err != nil {
		return nil, err
	}
	if !ok {
		// No decorator.  Just return the input value.
		return anyAsValue(any), nil
	}
	// Now see if there are additional decorators to apply as casts and
	// return value chain, wrapped if at all, as an astzed.Value.
	for {
		outer, ok, err := p.matchDecorator(nil, val)
		if err != nil {
			return nil, err
		}
		if !ok {
			return val, nil
		}
		val = outer
	}
}

// We pass both any and val in here to avoid having to backtrack.
// If we had proper backtracking, this would look a little more sensible.
func (p *Parser) matchDecorator(any astzed.Any, val astzed.Value) (astzed.Value, bool, error) {
	l := p.lexer
	ok, err := l.match('(')
	if err != nil || !ok {
		return nil, false, noEOF(err)
	}
	val, err = p.parseDecorator(any, val)
	if err != nil {
		return nil, false, err
	}
	ok, err = l.match(')')
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, p.error("mismatched parentheses while parsing type decorator")
	}
	return val, true, nil
}

func (p *Parser) parseDecorator(any astzed.Any, val astzed.Value) (astzed.Value, error) {
	l := p.lexer
	// We can have either:
	//   Case 1: =<name>
	//   Case 2: <type>
	// For case 2, there can be embedded typedefs created from the
	// descent into parseType.
	ok, err := l.match('=')
	if err != nil {
		return nil, err
	}
	if ok {
		name, err := l.scanTypeName()
		if name == "" || err != nil {
			return nil, p.error("bad short-form type definition")
		}
		return &astzed.DefValue{
			Kind:     "DefValue",
			Of:       any,
			TypeName: name,
		}, nil
	}
	typ, err := p.parseType()
	if err != nil {
		return nil, err
	}
	if any != nil {
		return &astzed.CastValue{
			Kind: "CastValue",
			Of:   anyAsValue(any),
			Type: typ,
		}, nil
	}
	return &astzed.CastValue{
		Kind: "CastValue",
		Of:   val,
		Type: typ,
	}, nil
}

func (p *Parser) matchPrimitive() (*astzed.Primitive, error) {
	if val, err := p.matchStringPrimitive(); val != nil || err != nil {
		return val, noEOF(err)
	}
	if val, err := p.matchBacktickString(); val != nil || err != nil {
		return val, noEOF(err)
	}
	l := p.lexer
	if err := l.skipSpace(); err != nil {
		return nil, noEOF(err)
	}
	s, err := l.peekPrimitive()
	if err != nil {
		return nil, noEOF(err)
	}
	if s == "" {
		return nil, nil
	}
	// Try to parse the string different ways.  This is not intended
	// to be performant.  ZNG provides performance for the ZSON data model.
	var typ string
	if s == "true" || s == "false" {
		typ = "bool"
	} else if s == "null" {
		typ = "null"
	} else if _, err := strconv.ParseInt(s, 10, 64); err == nil {
		typ = "int64"
	} else if _, err := strconv.ParseUint(s, 10, 64); err == nil {
		typ = "uint64"
	} else if _, err := strconv.ParseFloat(s, 64); err == nil {
		typ = "float64"
	} else if _, err := time.Parse(time.RFC3339Nano, s); err == nil {
		typ = "time"
	} else if _, err := nano.ParseDuration(s); err == nil {
		typ = "duration"
	} else if _, _, err := net.ParseCIDR(s); err == nil {
		typ = "net"
	} else if ip := net.ParseIP(s); ip != nil {
		typ = "ip"
	} else if len(s) >= 2 && s[0:2] == "0x" {
		if len(s) == 2 {
			typ = "bytes"
		} else if _, err := hex.DecodeString(s[2:]); err == nil {
			typ = "bytes"
		} else {
			return nil, err
		}
	} else {
		// no match
		return nil, nil
	}
	l.skip(len(s))
	return &astzed.Primitive{
		Kind: "Primitive",
		Type: typ,
		Text: s,
	}, nil
}

func (p *Parser) matchStringPrimitive() (*astzed.Primitive, error) {
	s, ok, err := p.matchString()
	if err != nil || !ok {
		return nil, noEOF(err)
	}
	return &astzed.Primitive{
		Kind: "Primitive",
		Type: "string",
		Text: s,
	}, nil
}

func (p *Parser) matchString() (string, bool, error) {
	l := p.lexer
	ok, err := l.match('"')
	if err != nil || !ok {
		return "", false, noEOF(err)
	}
	s, err := l.scanString()
	if err != nil {
		return "", false, p.errorf("string literal: %s", err)
	}
	ok, err = l.match('"')
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, p.error("mismatched string quotes")
	}
	return s, true, nil
}

var arrow = []byte("=>")

func (p *Parser) matchBacktickString() (*astzed.Primitive, error) {
	l := p.lexer
	keepIndentation := false
	ok, err := l.matchBytes(arrow)
	if err != nil {
		return nil, noEOF(err)
	}
	if ok {
		keepIndentation = true
	}
	ok, err = l.match('`')
	if err != nil || !ok {
		if err == nil && keepIndentation {
			err = errors.New("no backtick found following '=>'")
		}
		return nil, err
	}
	s, err := l.scanBacktickString(keepIndentation)
	if err != nil {
		return nil, p.error("parsing backtick string literal")
	}
	ok, err = l.match('`')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.error("mismatched string backticks")
	}
	return &astzed.Primitive{
		Kind: "Primitive",
		Type: "string",
		Text: s,
	}, nil
}

func (p *Parser) matchRecord() (*astzed.Record, error) {
	l := p.lexer
	if ok, err := l.match('{'); !ok || err != nil {
		return nil, noEOF(err)
	}
	fields, err := p.matchFields()
	if err != nil {
		return nil, err
	}
	ok, err := l.match('}')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.error("mismatched braces while parsing record type")
	}
	return &astzed.Record{
		Kind:   "Record",
		Fields: fields,
	}, nil
}

func (p *Parser) matchFields() ([]astzed.Field, error) {
	l := p.lexer
	var fields []astzed.Field
	seen := make(map[string]struct{})
	for {
		field, err := p.matchField()
		if err != nil {
			return nil, err
		}
		if field == nil {
			break
		}
		if _, ok := seen[field.Name]; !ok {
			fields = append(fields, *field)
		}
		seen[field.Name] = struct{}{}
		ok, err := l.match(',')
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	return fields, nil
}

func (p *Parser) matchField() (*astzed.Field, error) {
	l := p.lexer
	name, ok, err := p.matchSymbol()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	ok, err = l.match(':')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.errorf("no type name found for field %q", name)
	}
	val, err := p.ParseValue()
	if err != nil {
		return nil, err
	}
	return &astzed.Field{
		Name:  name,
		Value: val,
	}, nil
}

func (p *Parser) matchSymbol() (string, bool, error) {
	s, ok, err := p.matchString()
	if err != nil {
		return "", false, noEOF(err)
	}
	if ok {
		return s, true, nil
	}
	s, err = p.matchIdentifier()
	if err != nil || s == "" {
		return "", false, err
	}
	return s, true, nil
}

func (p *Parser) matchArray() (*astzed.Array, error) {
	l := p.lexer
	if ok, err := l.match('['); !ok || err != nil {
		return nil, noEOF(err)
	}
	vals, err := p.matchValueList()
	if err != nil {
		return nil, err
	}
	ok, err := l.match(']')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.error("mismatched brackets while parsing array type")
	}
	return &astzed.Array{
		Kind:     "Array",
		Elements: vals,
	}, nil
}

func (p *Parser) matchValueList() ([]astzed.Value, error) {
	l := p.lexer
	var vals []astzed.Value
	for {
		val, err := p.matchValue()
		if err != nil {
			return nil, err
		}
		if val == nil {
			break
		}
		vals = append(vals, val)
		ok, err := l.match(',')
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	return vals, nil
}

func (p *Parser) matchSetOrMap() (astzed.Any, error) {
	l := p.lexer
	if ok, err := l.match('|'); !ok || err != nil {
		return nil, noEOF(err)
	}
	isSet, err := l.matchTight('[')
	if err != nil {
		return nil, err
	}
	var val astzed.Any
	var which string
	if isSet {
		which = "set"
		vals, err := p.matchValueList()
		if err != nil {
			return nil, err
		}
		ok, err := l.match(']')
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, p.error("mismatched set value brackets")
		}
		val = &astzed.Set{
			Kind:     "Set",
			Elements: vals,
		}
	} else {
		ok, err := l.matchTight('{')
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, p.error("no '|[' or '|{' type bracket at '|' character")
		}
		which = "map"
		entries, err := p.matchMapEntries()
		if err != nil {
			return nil, err
		}
		ok, err = l.match('}')
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, p.error("mismatched map value brackets")
		}
		val = &astzed.Map{
			Kind:    "Map",
			Entries: entries,
		}
	}
	ok, err := l.matchTight('|')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.errorf("mismatched closing bracket while parsing %s value", which)
	}
	return val, nil

}

func (p *Parser) matchMapEntries() ([]astzed.Entry, error) {
	var entries []astzed.Entry
	for {
		entry, err := p.parseEntry()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			break
		}
		entries = append(entries, *entry)
		ok, err := p.lexer.match(',')
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	return entries, nil
}

func (p *Parser) parseEntry() (*astzed.Entry, error) {
	key, err := p.matchValue()
	if err != nil {
		return nil, err
	}
	if key == nil {
		// no match
		return nil, nil
	}
	ok, err := p.lexer.match(':')
	if err != nil {

		return nil, err
	}
	if !ok {
		return nil, p.error("no colon found after map key while parsing map entry")
	}
	val, err := p.ParseValue()
	if err != nil {
		return nil, err
	}
	return &astzed.Entry{
		Key:   key,
		Value: val,
	}, nil
}

func (p *Parser) matchEnum() (*astzed.Enum, error) {
	// We only detect identifier-style enum values even though they can
	// also be strings but we don't know that until the semantic check.
	name, err := p.matchIdentifier()
	if err != nil || name == "" {
		return nil, noEOF(err)
	}
	return &astzed.Enum{
		Kind: "Enum",
		Name: name,
	}, nil
}

func (p *Parser) matchTypeValue() (*astzed.TypeValue, error) {
	l := p.lexer
	if ok, err := l.match('('); !ok || err != nil {
		return nil, noEOF(err)
	}
	typ, err := p.parseType()
	if err != nil {
		return nil, err
	}
	ok, err := l.match(')')
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, p.error("mismatched parentheses while parsing type value")
	}
	return &astzed.TypeValue{
		Kind:  "TypeValue",
		Value: typ,
	}, nil
}

func ParsePrimitive(typeText, valText string) (zed.Value, error) {
	typ := zed.LookupPrimitive(typeText)
	if typ == nil {
		return zed.Value{}, fmt.Errorf("no such type: %s", typeText)
	}
	var b zcode.Builder
	if err := BuildPrimitive(&b, Primitive{Type: typ, Text: valText}); err != nil {
		return zed.Value{}, err
	}
	it := b.Bytes().Iter()
	bytes, _ := it.Next()
	return zed.Value{typ, bytes}, nil
}
