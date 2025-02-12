package anyio

import (
	"context"
	"io"

	"github.com/brimdata/zed"
	"github.com/brimdata/zed/pkg/storage"
	"github.com/brimdata/zed/zbuf"
	"github.com/brimdata/zed/zio"
)

// Open uses engine to open path for reading.  path is a local file path or a
// URI whose scheme is understood by engine.
func Open(ctx context.Context, zctx *zed.Context, engine storage.Engine, path string, opts ReaderOpts) (*zbuf.File, error) {
	uri, err := storage.ParseURI(path)
	if err != nil {
		return nil, err
	}
	ch := make(chan struct{})
	var zf *zbuf.File
	go func() {
		defer close(ch)
		var sr storage.Reader
		// Opening a fifo might block.
		sr, err = engine.Get(ctx, uri)
		if err != nil {
			return
		}
		// NewFile reads from sr, which might block.
		zf, err = NewFile(zctx, sr, path, opts)
		if err != nil {
			sr.Close()
		}
	}()
	select {
	case <-ch:
		return zf, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func NewFile(zctx *zed.Context, rc io.ReadCloser, path string, opts ReaderOpts) (*zbuf.File, error) {
	var err error
	r := io.Reader(rc)
	if opts.Format != "parquet" && opts.Format != "zst" {
		r = GzipReader(rc)
	}
	var zr zio.Reader
	if opts.Format == "" || opts.Format == "auto" {
		zr, err = NewReaderWithOpts(r, zctx, opts)
	} else {
		zr, err = lookupReader(r, zctx, opts)
	}
	if err != nil {
		return nil, err
	}

	return zbuf.NewFile(zr, rc, path), nil
}
