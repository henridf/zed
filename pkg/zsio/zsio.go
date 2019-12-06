package zsio

import (
	"io"

	"github.com/mccanne/zq/pkg/zsio/bzson"
	"github.com/mccanne/zq/pkg/zsio/ndjson"
	"github.com/mccanne/zq/pkg/zsio/table"
	"github.com/mccanne/zq/pkg/zsio/text"
	"github.com/mccanne/zq/pkg/zsio/zeek"
	"github.com/mccanne/zq/pkg/zsio/zjson"
	zsonio "github.com/mccanne/zq/pkg/zsio/zson"
	"github.com/mccanne/zq/pkg/zson"
	"github.com/mccanne/zq/pkg/zson/resolver"
)

type Writer struct {
	zson.WriteFlusher
	io.Closer
}

func NewWriter(writer zson.WriteFlusher, closer io.Closer) *Writer {
	return &Writer{
		WriteFlusher: writer,
		Closer:       closer,
	}
}

func (w *Writer) Close() error {
	err := w.Flush()
	cerr := w.Closer.Close()
	if err == nil {
		err = cerr
	}
	return err
}

func LookupWriter(format string, w io.WriteCloser, tc *text.Config) *Writer {
	var f zson.WriteFlusher
	switch format {
	default:
		return nil
	case "zson":
		f = zson.NopFlusher(zsonio.NewWriter(w))
	case "bzson":
		f = zson.NopFlusher(bzson.NewWriter(w))
	case "zeek":
		f = zson.NopFlusher(zeek.NewWriter(w))
	case "ndjson":
		f = zson.NopFlusher(ndjson.NewWriter(w))
	case "zjson":
		f = zson.NopFlusher(zjson.NewWriter(w))
	case "text":
		f = zson.NopFlusher(text.NewWriter(w, tc))
	case "table":
		f = table.NewWriter(w)
	}
	return &Writer{
		WriteFlusher: f,
		Closer:       w,
	}
}

func LookupReader(format string, r io.Reader, table *resolver.Table) zson.Reader {
	switch format {
	case "zson", "zeek":
		return zsonio.NewReader(r, table)
	case "ndjson":
		return ndjson.NewReader(r, table)
	case "zjson":
		return zjson.NewReader(r, table)
	case "bzson":
		return bzson.NewReader(r, table)
	}
	return nil
}

func Extension(format string) string {
	switch format {
	case "zson":
		return ".zson"
	case "zeek":
		return ".log"
	case "ndjson":
		return ".ndjson"
	case "zjson":
		return ".ndjson"
	case "text":
		return ".txt"
	case "table":
		return ".tbl"
	case "bzson":
		return ".bzson"
	default:
		return ""
	}
}
