package compress

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
)

var compressors = map[string]Algorithm{
	"gzip": Gzip,
	"zstd": Zstd,
}

func TestNewWithoutCompression(t *testing.T) {
	c, err := New(None)
	if err != nil {
		t.Fatalf("failed to build compressor: %v", err)
	}
	if c != nil {
		t.Errorf("got type %T, want no compressor", c)
	}
}

func TestNewRejectsUnsupported(t *testing.T) {
	c, err := New("gzipp")
	if err == nil {
		t.Error("got no error, want the algorithm rejected")
	}
	if c != nil {
		t.Errorf("got type %T, want no compressor", c)
	}
}

// TestRoundTrip asserts that each compressor emits a stream the
// matching decompressor reads back byte for byte.
func TestRoundTrip(t *testing.T) {
	cases := map[string]struct {
		algorithm  Algorithm
		payload    []byte
		decompress func(*testing.T, []byte) []byte
	}{
		"gzip":       {Gzip, testPayload(), gunzip},
		"zstd":       {Zstd, testPayload(), unzstd},
		"gzip empty": {Gzip, nil, gunzip},
		"zstd empty": {Zstd, nil, unzstd},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := newCompressor(t, tc.algorithm).Compress(tc.payload)
			if err != nil {
				t.Fatalf("failed to compress: %v", err)
			}
			if got := tc.decompress(t, out); !bytes.Equal(got, tc.payload) {
				t.Errorf("read back %d bytes, want the %d that were compressed", len(got), len(tc.payload))
			}
		})
	}
}

// TestCompressDiscardsPreviousPayload asserts that a payload is
// compressed into a truncated buffer, so that a call returns that
// payload alone rather than the one before it as well.
func TestCompressDiscardsPreviousPayload(t *testing.T) {
	cases := map[string]struct {
		algorithm  Algorithm
		decompress func(*testing.T, []byte) []byte
	}{
		"gzip": {Gzip, gunzip},
		"zstd": {Zstd, unzstd},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := newCompressor(t, tc.algorithm)
			compress(t, c, testPayload())

			want := []byte(`{"id":0}`)
			out, err := c.Compress(want)
			if err != nil {
				t.Fatalf("failed to compress: %v", err)
			}
			if got := tc.decompress(t, out); !bytes.Equal(got, want) {
				t.Errorf("read back %d bytes, want the %d that were compressed", len(got), len(want))
			}
		})
	}
}

// TestCompressReusesBuffer asserts that a payload is compressed into the buffer
// the previous writes grew, so that a steady stream of batches allocates nothing.
func TestCompressReusesBuffer(t *testing.T) {
	for name, algorithm := range compressors {
		t.Run(name, func(t *testing.T) {
			c := newCompressor(t, algorithm)
			compress(t, c, testPayload())

			capa := cap(*compressorBuffer(t, c))
			if capa == 0 {
				t.Fatal("buffer discarded")
			}
			compress(t, c, []byte(`foobar`))

			if got := cap(*compressorBuffer(t, c)); got != capa {
				t.Errorf("got capacity %d, want the %d it had grown to", got, capa)
			}
		})
	}
}

// TestCompressReleasesBuffer asserts that a buffer that has outgrown the
// size of the data it holds is released, so that one outsized batch cannot
// pin the extra allocated capacity for the life of the process.
func TestCompressReleasesBuffer(t *testing.T) {
	for name, algorithm := range compressors {
		t.Run(name, func(t *testing.T) {
			c := newCompressor(t, algorithm)
			b := compressorBuffer(t, c)
			*b = make([]byte, 1<<10, releaseThreshold+1)
			seeded := cap(*b)

			compress(t, c, testPayload())

			if got := cap(*b); got >= seeded {
				t.Errorf("got capacity %d, want less than the %d it started with", got, seeded)
			}
		})
	}
}

func TestCloseIdempotency(t *testing.T) {
	for name, algorithm := range compressors {
		t.Run(name, func(t *testing.T) {
			c := newCompressor(t, algorithm)
			for range 3 {
				if err := c.Close(); err != nil {
					t.Fatalf("failed to close: %v", err)
				}
			}
		})
	}
}

func newCompressor(t *testing.T, algorithm Algorithm) Compressor {
	t.Helper()

	c, err := New(algorithm)
	if err != nil {
		t.Fatalf("failed to build %s compressor: %v", algorithm, err)
	}
	if c == nil {
		t.Fatalf("no compressor was built for %s", algorithm)
	}
	return c
}

func compress(t *testing.T, c Compressor, payload []byte) {
	t.Helper()

	if _, err := c.Compress(payload); err != nil {
		t.Fatalf("failed to compress: %v", err)
	}
}

// compressorBuffer returns the buffer a compressor writes into.
func compressorBuffer(t *testing.T, c Compressor) *[]byte {
	t.Helper()

	switch c := c.(type) {
	case *gzipCompressor:
		return &c.dst.buf
	case *zstdCompressor:
		return &c.buf
	}
	t.Fatalf("got type %T, want a compressor", c)

	return nil
}

// testPayload returns a compressible payload of valid JSON.
func testPayload() []byte {
	buf := []byte{'['}

	for i := 0; len(buf) < 64<<10; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = fmt.Appendf(buf, `{"id":%d,"name":"item-%03d","active":true}`, i, i%128)
	}
	return append(buf, ']')
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()

	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("failed to read gzip stream: %v", err)
	}
	defer func() {
		_ = r.Close()
	}()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	return out
}

func unzstd(t *testing.T, b []byte) []byte {
	t.Helper()

	r, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatalf("failed to build zstd reader: %v", err)
	}
	defer r.Close()

	out, err := r.DecodeAll(b, nil)
	if err != nil {
		t.Fatalf("failed to decompress: %v", err)
	}
	return out
}
