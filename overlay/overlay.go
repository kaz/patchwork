package overlay

import (
	"errors"
	"fmt"
	"io"
	"os"
)

type (
	// Base is underlying original reader of overlayed reader. os.File can be Base.
	Base interface {
		io.Seeker
		io.ReaderAt
	}

	// Overlay is layered reader, which any section of its underlying reader can be modified non-destructively.
	// This behaves just like OverlayFS.
	//
	// Write operation to Overlay is treated as "pushing new layer". It will not break underlying base reader.
	// Reading from Overlay reflects write operations, without changing base reader.
	Overlay struct {
		base   Base
		cursor int64
		end    int64
		layers []*layer
	}

	layer struct {
		data   []byte
		offset int64
	}
)

// Create new instance with specified base layer.
func New(base Base) (*Overlay, error) {
	end, err := base.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to seek base: %w", err)
	}
	return &Overlay{base, 0, end, []*layer{}}, nil
}

// Create new instance which has os.File as base layer.
func NewFromFile(file string) (*Overlay, error) {
	fh, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return New(fh)
}

func (o *Overlay) Close() error {
	if closer, ok := interface{}(o).(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (o *Overlay) Size() int64 {
	return o.end
}

func (o *Overlay) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekEnd:
		o.cursor = offset + o.end
	case io.SeekStart:
		o.cursor = offset
	case io.SeekCurrent:
		o.cursor += offset
	}
	return o.cursor, nil
}

func (o *Overlay) Write(p []byte) (int, error) {
	n, err := o.WriteAt(p, o.cursor)
	o.cursor += int64(n)
	return n, err
}
func (o *Overlay) WriteAt(p []byte, off int64) (int, error) {
	if off > o.end {
		return 0, fmt.Errorf("sparse writing is prohibited: current end is %v, but attempted to write %v", o.end, off)
	}
	if off == o.end {
		o.end += int64(len(p))
	}
	o.layers = append(o.layers, &layer{p, off})
	return len(p), nil
}

func (o *Overlay) Read(p []byte) (int, error) {
	n, err := o.ReadAt(p, o.cursor)
	o.cursor += int64(n)
	return n, err
}
func (o *Overlay) ReadAt(p []byte, off int64) (int, error) {
	n, err := o.base.ReadAt(p, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("an error occurs in base layer: %w", err)
	}

	for _, layer := range o.layers {
		if layer.offset <= off && off <= layer.offset+int64(len(layer.data)) {
			n += copy(p, layer.data[off-layer.offset:])
		} else if off <= layer.offset && layer.offset <= off+int64(len(p)) {
			n += copy(p[layer.offset-off:], layer.data)
		}
	}

	if n < len(p) {
		return n, io.EOF
	} else {
		return len(p), nil
	}
}
