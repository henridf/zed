package zson_test

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/pkg/nano"
	"github.com/brimdata/zed/zio"
	"github.com/brimdata/zed/zio/zngio"
	"github.com/brimdata/zed/zio/zsonio"
	"github.com/brimdata/zed/zson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"inet.af/netaddr"
)

func toZSON(t *testing.T, rec *zed.Value) string {
	var buf strings.Builder
	require.NoError(t, zsonio.NewWriter(zio.NopCloser(&buf), zsonio.WriterOpts{}).Write(rec))
	return strings.TrimRight(buf.String(), "\n")
}

func boomerang(t *testing.T, in interface{}, out interface{}) {
	rec, err := zson.NewZNGMarshaler().MarshalRecord(in)
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := zngio.NewWriter(zio.NopCloser(&buf), zngio.WriterOpts{})
	err = zw.Write(rec)
	require.NoError(t, err)
	zctx := zed.NewContext()
	zr := zngio.NewReader(&buf, zctx)
	defer zr.Close()
	rec, err = zr.Read()
	require.NoError(t, err)
	err = zson.UnmarshalZNGRecord(rec, out)
	require.NoError(t, err)
}

func TestMarshalZNG(t *testing.T) {
	type S2 struct {
		Field2 string `zed:"f2"`
		Field3 int
	}
	type S1 struct {
		Field1  string
		Sub1    S2
		PField1 *bool
	}
	rec, err := zson.NewZNGMarshaler().MarshalRecord(S1{
		Field1: "value1",
		Sub1: S2{
			Field2: "value2",
			Field3: -1,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, `{Field1:"value1",Sub1:{f2:"value2",Field3:-1},PField1:null(bool)}`, toZSON(t, rec))
}

func TestMarshalMap(t *testing.T) {
	type s struct {
		Name string
		Map  map[string]int
	}
	cases := []s{
		{Name: "nil", Map: nil},
		{Name: "empty", Map: map[string]int{}},
		{Name: "nonempty", Map: map[string]int{"a": 1, "b": 2}},
	}
	for _, c := range cases {
		c := c
		var v s
		t.Run(c.Name, func(t *testing.T) {
			boomerang(t, c, &v)
			assert.Equal(t, c, v)
		})
	}
}

type ZNGThing struct {
	A string `zed:"a"`
	B int
}

type ZNGThings struct {
	Things []ZNGThing
}

func TestMarshalSlice(t *testing.T) {
	s := []ZNGThing{{"hello", 123}, {"world", 0}}
	r := ZNGThings{s}
	m := zson.NewZNGMarshaler()
	rec, err := m.MarshalRecord(r)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, `{Things:[{a:"hello",B:123},{a:"world",B:0}]}`, toZSON(t, rec))

	empty := []ZNGThing{}
	r2 := ZNGThings{empty}
	rec2, err := m.MarshalRecord(r2)
	require.NoError(t, err)
	require.NotNil(t, rec2)
	assert.Equal(t, "{Things:[]([{a:string,B:int64}])}", toZSON(t, rec2))
}

func TestMarshalNilSlice(t *testing.T) {
	type TestNilSlice struct {
		Name  string
		Slice []string
	}
	t1 := TestNilSlice{Name: "test"}
	var t2 TestNilSlice
	boomerang(t, t1, &t2)
	assert.Equal(t, t1, t2)
}

func TestMarshalEmptySlice(t *testing.T) {
	type TestNilSlice struct {
		Name  string
		Slice []string
	}
	t1 := TestNilSlice{Name: "test", Slice: []string{}}
	var t2 TestNilSlice
	boomerang(t, t1, &t2)
	assert.Equal(t, t1, t2)
}

func TestMarshalTime(t *testing.T) {
	type TestTime struct {
		Ts nano.Ts
	}
	t1 := TestTime{Ts: nano.Now()}
	var t2 TestTime
	boomerang(t, t1, &t2)
	assert.Equal(t, t1, t2)
}

type TestIP struct {
	Addr netaddr.IP
}

func TestIPType(t *testing.T) {
	s := TestIP{Addr: netaddr.MustParseIP("192.168.1.1")}
	zctx := zed.NewContext()
	m := zson.NewZNGMarshalerWithContext(zctx)
	rec, err := m.MarshalRecord(s)
	require.NoError(t, err)
	require.NotNil(t, rec)

	assert.Equal(t, "{Addr:192.168.1.1}", toZSON(t, rec))

	var tip TestIP
	err = zson.UnmarshalZNGRecord(rec, &tip)
	require.NoError(t, err)
	require.Equal(t, s, tip)
}

func TestUnmarshalRecord(t *testing.T) {
	type T3 struct {
		T3f1 int32
		T3f2 float32
	}
	type T2 struct {
		T2f1 T3
		T2f2 string
	}
	type T1 struct {
		T1f1 *T2 `zed:"top"`
	}
	v1 := T1{
		T1f1: &T2{T2f1: T3{T3f1: 1, T3f2: 1.0}, T2f2: "t2f2-string1"},
	}
	rec, err := zson.NewZNGMarshaler().MarshalRecord(v1)
	require.NoError(t, err)
	require.NotNil(t, rec)

	const expected = `{top:{T2f1:{T3f1:1(int32),T3f2:1.(float32)},T2f2:"t2f2-string1"}}`
	require.Equal(t, expected, toZSON(t, rec))

	zctx := zed.NewContext()
	rec, err = zsonio.NewReader(strings.NewReader(expected), zctx).Read()
	require.NoError(t, err)

	var v2 T1
	err = zson.UnmarshalZNGRecord(rec, &v2)
	require.NoError(t, err)
	require.Equal(t, v1, v2)

	type T4 struct {
		T4f1 *T2 `zed:"top"`
	}
	var v3 *T4
	err = zson.UnmarshalZNGRecord(rec, &v3)
	require.NoError(t, err)
	require.NotNil(t, v3)
	require.NotNil(t, v3.T4f1)
	require.Equal(t, *v1.T1f1, *v3.T4f1)
}

func TestUnmarshalSlice(t *testing.T) {
	type T1 struct {
		T1f1 []bool
	}
	v1 := T1{
		T1f1: []bool{true, false, true},
	}
	zctx := zed.NewContext()
	rec, err := zson.NewZNGMarshalerWithContext(zctx).MarshalRecord(v1)
	require.NoError(t, err)
	require.NotNil(t, rec)

	var v2 T1
	err = zson.UnmarshalZNGRecord(rec, &v2)
	require.NoError(t, err)
	require.Equal(t, v1, v2)

	type T2 struct {
		Field1 []*int
	}
	intp := func(x int) *int { return &x }
	v3 := T2{
		Field1: []*int{intp(1), intp(2)},
	}
	zctx = zed.NewContext()
	rec, err = zson.NewZNGMarshalerWithContext(zctx).MarshalRecord(v3)
	require.NoError(t, err)
	require.NotNil(t, rec)

	var v4 T2
	err = zson.UnmarshalZNGRecord(rec, &v4)
	require.NoError(t, err)
	require.Equal(t, v1, v2)
}

type testMarshaler string

func (m testMarshaler) MarshalZNG(mc *zson.MarshalZNGContext) (zed.Type, error) {
	return mc.MarshalValue("marshal-" + string(m))
}

func (m *testMarshaler) UnmarshalZNG(mc *zson.UnmarshalZNGContext, zv zed.Value) error {
	var s string
	if err := mc.Unmarshal(zv, &s); err != nil {
		return err
	}
	ss := strings.Split(s, "-")
	if len(ss) != 2 && ss[0] != "marshal" {
		return errors.New("bad value")
	}
	*m = testMarshaler(ss[1])
	return nil
}

func TestMarshalInterface(t *testing.T) {
	type rectype struct {
		M1 *testMarshaler
		M2 testMarshaler
	}
	m1 := testMarshaler("m1")
	r1 := rectype{M1: &m1, M2: testMarshaler("m2")}
	rec, err := zson.NewZNGMarshaler().MarshalRecord(r1)
	require.NoError(t, err)
	require.NotNil(t, rec)
	assert.Equal(t, `{M1:"marshal-m1",M2:"marshal-m2"}`, toZSON(t, rec))

	var r2 rectype
	err = zson.UnmarshalZNGRecord(rec, &r2)
	require.NoError(t, err)
	assert.Equal(t, "m1", string(*r2.M1))
	assert.Equal(t, "m2", string(r2.M2))
}

func TestMarshalArray(t *testing.T) {
	type rectype struct {
		A1 [2]int8
		A2 *[2]string
		A3 [][2]byte
	}
	a2 := &[2]string{"foo", "bar"}
	r1 := rectype{A1: [2]int8{1, 2}, A2: a2} // A3 left as nil
	rec, err := zson.NewZNGMarshaler().MarshalRecord(r1)
	require.NoError(t, err)
	require.NotNil(t, rec)
	const expected = `{A1:[1(int8),2(int8)],A2:["foo","bar"],A3:null([bytes])}`
	assert.Equal(t, expected, toZSON(t, rec))

	var r2 rectype
	err = zson.UnmarshalZNGRecord(rec, &r2)
	require.NoError(t, err)
	assert.Equal(t, r1.A1, r2.A1)
	assert.Equal(t, *r2.A2, *r2.A2)
	assert.Len(t, r2.A3, 0)
}

func TestNumbers(t *testing.T) {
	type rectype struct {
		I    int
		I8   int8
		I16  int16
		I32  int32
		I64  int64
		U    uint
		UI8  uint8
		UI16 uint16
		UI32 uint32
		UI64 uint64
		F32  float32
		F64  float64
	}
	r1 := rectype{
		I:    math.MinInt64,
		I8:   math.MinInt8,
		I16:  math.MinInt16,
		I32:  math.MinInt32,
		I64:  math.MinInt64,
		U:    math.MaxUint64,
		UI8:  math.MaxUint8,
		UI16: math.MaxUint16,
		UI32: math.MaxUint32,
		UI64: math.MaxUint64,
		F32:  math.MaxFloat32,
		F64:  math.MaxFloat64,
	}
	rec, err := zson.NewZNGMarshaler().MarshalRecord(r1)
	require.NoError(t, err)
	require.NotNil(t, rec)
	const expected = "{I:-9223372036854775808,I8:-128(int8),I16:-32768(int16),I32:-2147483648(int32),I64:-9223372036854775808,U:18446744073709551615(uint64),UI8:255(uint8),UI16:65535(uint16),UI32:4294967295(uint32),UI64:18446744073709551615(uint64),F32:3.4028235e+38(float32),F64:1.7976931348623157e+308}"
	assert.Equal(t, expected, toZSON(t, rec))

	var r2 rectype
	err = zson.UnmarshalZNGRecord(rec, &r2)
	require.NoError(t, err)
	assert.Equal(t, r1, r2)
}

func TestCustomRecord(t *testing.T) {
	vals := []interface{}{
		ZNGThing{"hello", 123},
		99,
	}
	m := zson.NewZNGMarshaler()
	rec, err := m.MarshalCustom([]string{"foo", "bar"}, vals)
	require.NoError(t, err)
	assert.Equal(t, `{foo:{a:"hello",B:123},bar:99}`, toZSON(t, rec))

	vals = []interface{}{
		ZNGThing{"hello", 123},
		nil,
	}
	rec, err = m.MarshalCustom([]string{"foo", "bar"}, vals)
	require.NoError(t, err)
	assert.Equal(t, `{foo:{a:"hello",B:123},bar:null}`, toZSON(t, rec))
}

type ThingTwo struct {
	C string `zed:"c"`
}

type ThingaMaBob interface {
	Who() string
}

func (t *ZNGThing) Who() string { return t.A }
func (t *ThingTwo) Who() string { return t.C }

func Make(which int) ThingaMaBob {
	if which == 1 {
		return &ZNGThing{A: "It's a thing one"}
	}
	if which == 2 {
		return &ThingTwo{"It's a thing two"}
	}
	return nil
}

type Rolls []int

func TestInterfaceZNGMarshal(t *testing.T) {
	t1 := Make(2)
	m := zson.NewZNGMarshaler()
	m.Decorate(zson.StylePackage)
	zv, err := m.Marshal(t1)
	require.NoError(t, err)
	assert.Equal(t, "zson_test.ThingTwo={c:string}", zson.String(zv.Type))

	m.Decorate(zson.StyleSimple)
	rolls := Rolls{1, 2, 3}
	zv, err = m.Marshal(rolls)
	require.NoError(t, err)
	assert.Equal(t, "Rolls=[int64]", zson.String(zv.Type))

	m.Decorate(zson.StyleFull)
	zv, err = m.Marshal(rolls)
	require.NoError(t, err)
	assert.Equal(t, "github.com/brimdata/zed/zson_test.Rolls=[int64]", zson.String(zv.Type))

	plain := []int32{1, 2, 3}
	zv, err = m.Marshal(plain)
	require.NoError(t, err)
	assert.Equal(t, "[int32]", zson.String(zv.Type))
}

func TestInterfaceUnmarshal(t *testing.T) {
	t1 := Make(1)
	m := zson.NewZNGMarshaler()
	m.Decorate(zson.StylePackage)
	zv, err := m.Marshal(t1)
	require.NoError(t, err)
	assert.Equal(t, "zson_test.ZNGThing={a:string,B:int64}", zson.String(zv.Type))

	u := zson.NewZNGUnmarshaler()
	u.Bind(ZNGThing{}, ThingTwo{})
	var thing ThingaMaBob
	require.NoError(t, err)
	err = u.Unmarshal(*zv, &thing)
	require.NoError(t, err)
	assert.Equal(t, "It's a thing one", thing.Who())

	var thingI interface{}
	err = u.Unmarshal(*zv, &thingI)
	require.NoError(t, err)
	actualThing, ok := thingI.(*ZNGThing)
	assert.Equal(t, true, ok)
	assert.Equal(t, t1, actualThing)

	u2 := zson.NewZNGUnmarshaler()
	var genericThing interface{}
	err = u2.Unmarshal(*zv, &genericThing)
	require.Error(t, err)
	assert.Equal(t, "unmarshaling records into interface value requires type binding", err.Error())
}

func TestBindings(t *testing.T) {
	t1 := Make(1)
	m := zson.NewZNGMarshaler()
	m.NamedBindings([]zson.Binding{
		{"SpecialThingOne", &ZNGThing{}},
		{"SpecialThingTwo", &ThingTwo{}},
	})
	zv, err := m.Marshal(t1)
	require.NoError(t, err)
	assert.Equal(t, "SpecialThingOne={a:string,B:int64}", zson.String(zv.Type))

	u := zson.NewZNGUnmarshaler()
	u.NamedBindings([]zson.Binding{
		{"SpecialThingOne", &ZNGThing{}},
		{"SpecialThingTwo", &ThingTwo{}},
	})
	var thing ThingaMaBob
	require.NoError(t, err)
	err = u.Unmarshal(*zv, &thing)
	require.NoError(t, err)
	assert.Equal(t, "It's a thing one", thing.Who())
}

func TestEmptyInterface(t *testing.T) {
	zv, err := zson.MarshalZNG(int8(123))
	require.NoError(t, err)
	assert.Equal(t, "int8", zson.String(zv.Type))

	var v interface{}
	err = zson.UnmarshalZNG(*zv, &v)
	require.NoError(t, err)
	i, ok := v.(int8)
	assert.Equal(t, true, ok)
	assert.Equal(t, int8(123), i)

	var actual int8
	err = zson.UnmarshalZNG(*zv, &actual)
	require.NoError(t, err)
	assert.Equal(t, int8(123), actual)
}

type CustomInt8 int8

func TestNamedNormal(t *testing.T) {
	t1 := CustomInt8(88)
	m := zson.NewZNGMarshaler()
	m.Decorate(zson.StyleSimple)

	zv, err := m.Marshal(t1)
	require.NoError(t, err)
	assert.Equal(t, "CustomInt8=int8", zson.String(zv.Type))

	var actual CustomInt8
	u := zson.NewZNGUnmarshaler()
	u.Bind(CustomInt8(0))
	err = u.Unmarshal(*zv, &actual)
	require.NoError(t, err)
	assert.Equal(t, t1, actual)

	var actualI interface{}
	err = u.Unmarshal(*zv, &actualI)
	require.NoError(t, err)
	cast, ok := actualI.(CustomInt8)
	assert.Equal(t, true, ok)
	assert.Equal(t, t1, cast)
}

type EmbeddedA struct {
	A ThingaMaBob
}

type EmbeddedB struct {
	A interface{}
}

func TestEmbeddedInterface(t *testing.T) {
	t1 := &EmbeddedA{
		A: Make(1),
	}
	m := zson.NewZNGMarshaler()
	m.Decorate(zson.StyleSimple)
	zv, err := m.Marshal(t1)
	require.NoError(t, err)
	assert.Equal(t, "EmbeddedA={A:ZNGThing={a:string,B:int64}}", zson.String(zv.Type))

	u := zson.NewZNGUnmarshaler()
	u.Bind(ZNGThing{}, ThingTwo{})
	var actual EmbeddedA
	require.NoError(t, err)
	err = u.Unmarshal(*zv, &actual)
	require.NoError(t, err)
	assert.Equal(t, "It's a thing one", actual.A.Who())

	var actualB EmbeddedB
	require.NoError(t, err)
	err = u.Unmarshal(*zv, &actualB)
	require.NoError(t, err)
	thingB, ok := actualB.A.(*ZNGThing)
	assert.Equal(t, true, ok)
	assert.Equal(t, "It's a thing one", thingB.Who())
}

func TestMultipleZedValues(t *testing.T) {
	bytes := []byte("foo")
	u := zson.NewZNGUnmarshaler()
	var foo zed.Value
	err := u.Unmarshal(*zed.NewValue(zed.TypeString, bytes), &foo)
	require.NoError(t, err)
	// clobber bytes slice
	copy(bytes, []byte("bar"))
	var bar zed.Value
	err = u.Unmarshal(*zed.NewValue(zed.TypeString, bytes), &bar)
	require.NoError(t, err)
	assert.Equal(t, "foo", string(foo.Bytes))
	assert.Equal(t, "bar", string(bar.Bytes))
}
