package zjsonio

import (
	"errors"
	"fmt"

	"github.com/brimdata/zed"
	astzed "github.com/brimdata/zed/compiler/ast/zed"
)

type encoder map[zed.Type]string

func (e encoder) encodeType(zctx *zed.Context, typ zed.Type) astzed.Type {
	if name, ok := e[typ]; ok {
		return &astzed.TypeName{
			Kind: "typename",
			Name: name,
		}
	}
	switch typ := typ.(type) {
	case *zed.TypeNamed:
		name := typ.Name
		t := e.encodeType(zctx, typ.Type)
		e[typ] = name
		return &astzed.TypeDef{
			Kind: "typedef",
			Name: name,
			Type: t,
		}
	case *zed.TypeRecord:
		return e.encodeTypeRecord(zctx, typ)
	case *zed.TypeArray:
		return &astzed.TypeArray{
			Kind: "array",
			Type: e.encodeType(zctx, typ.Type),
		}
	case *zed.TypeSet:
		return &astzed.TypeSet{
			Kind: "set",
			Type: e.encodeType(zctx, typ.Type),
		}
	case *zed.TypeUnion:
		return e.encodeTypeUnion(zctx, typ)
	case *zed.TypeEnum:
		return e.encodeTypeEnum(zctx, typ)
	case *zed.TypeMap:
		return &astzed.TypeMap{
			Kind:    "map",
			KeyType: e.encodeType(zctx, typ.KeyType),
			ValType: e.encodeType(zctx, typ.ValType),
		}
	case *zed.TypeError:
		return &astzed.TypeError{
			Kind: "error",
			Type: e.encodeType(zctx, typ.Type),
		}
	default:
		return &astzed.TypePrimitive{
			Kind: "primitive",
			Name: zed.PrimitiveName(typ),
		}
	}
}

func (e encoder) encodeTypeRecord(zctx *zed.Context, typ *zed.TypeRecord) *astzed.TypeRecord {
	var fields []astzed.TypeField
	for _, c := range typ.Columns {
		typ := e.encodeType(zctx, c.Type)
		fields = append(fields, astzed.TypeField{c.Name, typ})
	}
	return &astzed.TypeRecord{
		Kind:   "record",
		Fields: fields,
	}
}

func (e encoder) encodeTypeEnum(zctx *zed.Context, typ *zed.TypeEnum) *astzed.TypeEnum {
	panic("issue 2508")
}

func (e encoder) encodeTypeUnion(zctx *zed.Context, union *zed.TypeUnion) *astzed.TypeUnion {
	var types []astzed.Type
	for _, t := range union.Types {
		types = append(types, e.encodeType(zctx, t))
	}
	return &astzed.TypeUnion{
		Kind:  "union",
		Types: types,
	}
}

type decoder map[string]zed.Type

func (d decoder) decodeType(zctx *zed.Context, typ astzed.Type) (zed.Type, error) {
	switch typ := typ.(type) {
	case *astzed.TypeRecord:
		return d.decodeTypeRecord(zctx, typ)
	case *astzed.TypeArray:
		t, err := d.decodeType(zctx, typ.Type)
		if err != nil {
			return nil, err
		}
		return zctx.LookupTypeArray(t), nil
	case *astzed.TypeSet:
		t, err := d.decodeType(zctx, typ.Type)
		if err != nil {
			return nil, err
		}
		return zctx.LookupTypeSet(t), nil
	case *astzed.TypeUnion:
		return d.decodeTypeUnion(zctx, typ)
	case *astzed.TypeEnum:
		return d.decodeTypeEnum(zctx, typ)
	case *astzed.TypeMap:
		return d.decodeTypeMap(zctx, typ)
	case *astzed.TypeName:
		t := zctx.LookupTypeDef(typ.Name)
		if typ == nil {
			return nil, fmt.Errorf("ZJSON decoder: no such type name: %s", typ.Name)
		}
		return t, nil
	case *astzed.TypeDef:
		t, err := d.decodeType(zctx, typ.Type)
		if err != nil {
			return nil, err
		}
		d[typ.Name] = t
		if !zed.IsIdentifier(typ.Name) {
			return t, nil
		}
		return zctx.LookupTypeNamed(typ.Name, t)
	case *astzed.TypeError:
		t, err := d.decodeType(zctx, typ.Type)
		if err != nil {
			return nil, err
		}
		return zctx.LookupTypeError(t), nil
	case *astzed.TypePrimitive:
		t := zed.LookupPrimitive(typ.Name)
		if t == nil {
			return nil, errors.New("ZJSON unknown type: " + typ.Name)
		}
		return t, nil
	}
	return nil, fmt.Errorf("ZJSON unknown type: %T", typ)
}

func (d decoder) decodeTypeRecord(zctx *zed.Context, typ *astzed.TypeRecord) (*zed.TypeRecord, error) {
	columns := make([]zed.Column, 0, len(typ.Fields))
	for _, field := range typ.Fields {
		typ, err := d.decodeType(zctx, field.Type)
		if err != nil {
			return nil, err
		}
		column := zed.Column{
			Name: field.Name,
			Type: typ,
		}
		columns = append(columns, column)
	}
	return zctx.LookupTypeRecord(columns)
}

func (d decoder) decodeTypeUnion(zctx *zed.Context, union *astzed.TypeUnion) (*zed.TypeUnion, error) {
	var types []zed.Type
	for _, t := range union.Types {
		typ, err := d.decodeType(zctx, t)
		if err != nil {
			return nil, err
		}
		types = append(types, typ)
	}
	return zctx.LookupTypeUnion(types), nil
}

func (d decoder) decodeTypeMap(zctx *zed.Context, m *astzed.TypeMap) (*zed.TypeMap, error) {
	keyType, err := d.decodeType(zctx, m.KeyType)
	if err != nil {
		return nil, err
	}
	valType, err := d.decodeType(zctx, m.ValType)
	if err != nil {
		return nil, err
	}
	return zctx.LookupTypeMap(keyType, valType), nil
}

func (d decoder) decodeTypeEnum(zctx *zed.Context, enum *astzed.TypeEnum) (*zed.TypeEnum, error) {
	return nil, errors.New("TBD: issue #2508")
}
