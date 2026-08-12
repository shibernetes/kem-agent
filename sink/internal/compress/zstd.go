package compress

import (
	"fmt"

	"github.com/klauspost/compress/zstd"

	"github.com/shibernetes/kem-agent/internal/buffer"
)

const (
	zstdWindowSize  = 1 << 20 // 1 MiB
	zstdConcurrency = 1
)

var _ Compressor = (*zstdCompressor)(nil)

// A zstdCompressor compresses payloads with zstd.
type zstdCompressor struct {
	encoder *zstd.Encoder
	buf     []byte
}

// newZstdCompressor returns a zstd compressor.
func newZstdCompressor() (*zstdCompressor, error) {
	encoder, err := zstd.NewWriter(
		nil,
		zstd.WithWindowSize(zstdWindowSize),
		zstd.WithEncoderConcurrency(zstdConcurrency),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	return &zstdCompressor{encoder: encoder}, nil
}

// Compress implements the [Compressor] interface.
func (c *zstdCompressor) Compress(src []byte) ([]byte, error) {
	c.buf = buffer.Shrink(c.buf, releaseThreshold)[:0]
	c.buf = c.encoder.EncodeAll(src, c.buf)

	return c.buf, nil
}

// Close implements the [Compressor] interface.
func (c *zstdCompressor) Close() error {
	return c.encoder.Close()
}
