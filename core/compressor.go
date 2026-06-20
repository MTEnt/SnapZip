package core

import (
	"bytes"
	"compress/zlib"
	"sync"

	"github.com/klauspost/compress/zstd"
)

type Compressor interface {
	Compress(data []byte) int
	Name() string
}

// ZstdCompressor wraps a Klauspost Zstd encoder for high-performance, allocation-free compression
type ZstdCompressor struct {
	encoder *zstd.Encoder
}

func NewZstdCompressor(level zstd.EncoderLevel) (*ZstdCompressor, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(level))
	if err != nil {
		return nil, err
	}
	return &ZstdCompressor{encoder: enc}, nil
}

func (zc *ZstdCompressor) Name() string { return "zstd" }

func (zc *ZstdCompressor) Compress(data []byte) int {
	return len(zc.encoder.EncodeAll(data, nil))
}

// ZlibCompressor wraps the standard zlib library using a recycled pool of writers
type ZlibCompressor struct{}

type zlibWrapper struct {
	buf *bytes.Buffer
	w   *zlib.Writer
}

var zlibPool = sync.Pool{
	New: func() interface{} {
		var buf bytes.Buffer
		w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
		return &zlibWrapper{
			buf: &buf,
			w:   w,
		}
	},
}

func (zc ZlibCompressor) Name() string { return "zlib" }

func (zc ZlibCompressor) Compress(data []byte) int {
	wrapper := zlibPool.Get().(*zlibWrapper)
	defer zlibPool.Put(wrapper)

	wrapper.buf.Reset()
	wrapper.w.Reset(wrapper.buf)
	_, _ = wrapper.w.Write(data)
	_ = wrapper.w.Close()
	return wrapper.buf.Len()
}

// CalculateQND computes the Query-Normalized Compression Distance between a prompt and document content
func CalculateQND(comp Compressor, prompt string, content string) float64 {
	x := []byte(prompt)
	y := []byte(content)
	xy := []byte(content + "\n" + prompt)

	cx := comp.Compress(x)
	if cx == 0 {
		return 1.0
	}
	cy := comp.Compress(y)
	cxy := comp.Compress(xy)

	return float64(cxy-cy) / float64(cx)
}
