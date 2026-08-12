package compress

const (
	// releaseThreshold is the capacity in bytes above which a
	// compressor releases the buffer it compressed into.
	releaseThreshold = 4 << 20 // 4 MiB
)

// A Compressor compresses a composed payload before it is sent,
// reusing its writer and its buffer across calls.
type Compressor interface {
	// Compress returns the compressed payload, valid until the next call.
	Compress([]byte) ([]byte, error)

	// Close releases the resources the compressor holds.
	Close() error
}

// New returns a compressor for the algorithm, or nil for [None].
// An unsupported algorithm returns an error, so that a misspelled one
// never disables compression silently.
func New(a Algorithm) (Compressor, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	switch a {
	case Gzip:
		return newGzipCompressor(), nil
	case Zstd:
		c, err := newZstdCompressor()
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return nil, nil
}
