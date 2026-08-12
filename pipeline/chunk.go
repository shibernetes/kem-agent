package pipeline

import (
	"math"
	"sync"

	"github.com/shibernetes/kem-agent/sink"
)

const (
	// frameHeaderLen is the width of the length prefix a queue
	// writes ahead of every frame.
	frameHeaderLen = 4

	// maxFrameLen is the longest frame that prefix can describe.
	maxFrameLen = math.MaxUint32
)

// chunkPool holds the idle chunks of every sink's queue.
// Sharing them means memory usage follows the concurrent peak across
// sinks rather than the sum of what each of them has ever held.
var chunkPool = sync.Pool{
	// The buffer is allocated apart from the chunk so that it lands
	// on a size-class span of its own, which a struct carrying it
	// inline would miss by the width of its own fields.
	New: func() any {
		return &chunk{buf: make([]byte, sink.QueueChunkSize)}
	},
}

// chunk is a fixed-size buffer a queue writes its frames in.
// Its content is the bytes between the read and the write offset,
// and it is fully drained once the two meet.
//
// A frame may span several chunks, and only its length prefix
// is always contiguous. The writer leaves the last few bytes of
// a chunk unused rather than split a prefix across two of them.
type chunk struct {
	buf  []byte
	r    int
	w    int
	next *chunk
}

// newChunk takes a chunk from the shared pool.
func newChunk() *chunk {
	return chunkPool.Get().(*chunk)
}

// release puts c back in the shared pool.
// Only its offsets are cleared, since a write overwrites
// and a read never goes past the write offset.
func (c *chunk) release() {
	c.r = 0
	c.w = 0
	c.next = nil
	chunkPool.Put(c)
}

// free returns the space left to write in c.
func (c *chunk) free() int {
	return len(c.buf) - c.w
}

// unread returns the content left to read in c.
func (c *chunk) unread() int {
	return c.w - c.r
}
