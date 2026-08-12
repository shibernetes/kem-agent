package compress

import (
	"github.com/klauspost/compress/gzip"

	"github.com/shibernetes/kem-agent/internal/buffer"
)

var _ Compressor = (*gzipCompressor)(nil)

// A gzipCompressor compresses payloads with gzip.
type gzipCompressor struct {
	writer *gzip.Writer
	dst    appender
}

// newGzipCompressor returns a gzip compressor.
// The writer is reset for each payload rather than recreated, since a
// new one allocates window and hash tables larger than most payloads.
func newGzipCompressor() *gzipCompressor {
	return &gzipCompressor{writer: gzip.NewWriter(nil)}
}

// Compress implements the [Compressor] interface.
func (c *gzipCompressor) Compress(src []byte) ([]byte, error) {
	c.dst.buf = buffer.Shrink(c.dst.buf, releaseThreshold)[:0]
	c.writer.Reset(&c.dst)

	if _, err := c.writer.Write(src); err != nil {
		return nil, err
	}
	// Closing writes the trailer the stream ends with,
	// and leaves the writer reusable through Reset.
	if err := c.writer.Close(); err != nil {
		return nil, err
	}
	return c.dst.buf, nil
}

// Close implements the [Compressor] interface.
func (c *gzipCompressor) Close() error {
	return nil
}

// An appender adapts a reused byte slice to [io.Writer].
type appender struct {
	buf []byte
}

// Write implements the [io.Writer] interface.
func (a *appender) Write(p []byte) (int, error) {
	a.buf = append(a.buf, p...)
	return len(p), nil
}
