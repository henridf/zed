package zson_test

import (
	"encoding/json"
	"testing"

	"github.com/brimdata/zed"
	astzed "github.com/brimdata/zed/compiler/ast/zed"
	"github.com/brimdata/zed/pkg/fs"
	"github.com/brimdata/zed/zcode"
	"github.com/brimdata/zed/zson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(path string) (astzed.Value, error) {
	file, err := fs.Open(path)
	if err != nil {
		return nil, err
	}
	return zson.NewParser(file).ParseValue()
}

const testFile = "test.zson"

func TestZsonParser(t *testing.T) {
	val, err := parse(testFile)
	require.NoError(t, err)
	s, err := json.MarshalIndent(val, "", "    ")
	require.NoError(t, err)
	assert.NotEqual(t, s, "")
}

func analyze(zctx *zed.Context, path string) (zson.Value, error) {
	val, err := parse(path)
	if err != nil {
		return nil, err
	}
	analyzer := zson.NewAnalyzer()
	return analyzer.ConvertValue(zctx, val)
}

func TestZsonAnalyzer(t *testing.T) {
	zctx := zed.NewContext()
	val, err := analyze(zctx, testFile)
	require.NoError(t, err)
	assert.NotNil(t, val)
}

func TestZsonBuilder(t *testing.T) {
	zctx := zed.NewContext()
	val, err := analyze(zctx, testFile)
	require.NoError(t, err)
	b := zcode.NewBuilder()
	zv, err := zson.Build(b, val)
	require.NoError(t, err)
	rec := zed.NewValue(zv.Type.(*zed.TypeRecord), zv.Bytes)
	zv, err = rec.Access("a")
	require.NoError(t, err)
	assert.Equal(t, "[string]: [(31)(32)(33)]", zv.String())
}

func TestParseValueStringEscapeSequences(t *testing.T) {
	cases := []struct {
		in       string
		expected string
	}{
		{` "\"\\//\b\f\n\r\t" `, "\"\\//\b\f\n\r\t"},
		{` "\u0000\u000A\u000b" `, "\u0000\u000A\u000b"},
	}
	for _, c := range cases {
		val, err := zson.ParseValue(zed.NewContext(), c.in)
		assert.NoError(t, err)
		assert.Equal(t, zed.NewString(c.expected), &val, "in %q", c.in)
	}
}

func TestParseValueErrors(t *testing.T) {
	cases := []struct {
		in            string
		expectedError string
	}{
		{" \"\n\" ", `parse error: string literal: unescaped line break`},
		{` "`, `parse error: string literal: EOF`},
		{` "\`, `parse error: string literal: EOF`},
		{` "\u`, `parse error: string literal: EOF`},
		{` "\u" `, `parse error: string literal: bad \uXXXX escape sequence`},
		{` "\u0" `, `parse error: string literal: bad \uXXXX escape sequence`},
		{` "\u00" `, `parse error: string literal: bad \uXXXX escape sequence`},
		{` "\u000" `, `parse error: string literal: bad \uXXXX escape sequence`},
		{` "\u000g" `, `parse error: string literal: bad \uXXXX escape sequence`},
		// Go's \UXXXXXXXX is not recognized.
		{` "\U00000000" `, `parse error: string literal: bad \C escape sequence`},
		// Go's \xXX is not recognized.
		{` "\x00" `, `parse error: string literal: bad \C escape sequence`},
		// Go's \a is not recognized.
		{` "\a" `, `parse error: string literal: bad \C escape sequence`},
		// Go's \v is not recognized.
		{` "\v" `, `parse error: string literal: bad \C escape sequence`},
	}
	for _, c := range cases {
		_, err := zson.ParseValue(zed.NewContext(), c.in)
		assert.EqualError(t, err, c.expectedError, "in: %q", c.in)
	}
}
