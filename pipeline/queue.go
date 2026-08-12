package pipeline

import (
	"encoding/binary"
	"sync"
)

// Queue is the bounded buffer between a sink's producers and its
// drainer. Encoded frames wait here in arrival order, and when it
// fills up the oldest ones make way for the newest.
//
// Frames are held in a chain of chunks taken from a shared pool,
// so a queue costs what it holds rather than what it is allowed
// to hold, and it grows and shrinks without copying anything.
type Queue struct {
	ceiling    int
	mu         sync.Mutex
	head       *chunk
	tail       *chunk
	frameSize  int
	frameCount int
}

// EvictedFrames reports the result of frame eviction on push.
// Its zero value indicates that no frames were evicted.
type EvictedFrames struct {
	Count int
	Bytes int
}

// NewQueue returns a queue holding at most n bytes of frames.
//
// The limit is a ceiling rather than a reservation, so a queue takes
// chunks as it fills and releases them as it drains, down to the one
// it always keeps. It counts frame content alone, leaving the memory
// a queue holds a little above it: four bytes for every frame, and
// the tail its last chunk has yet to be written to.
func NewQueue(n int) *Queue {
	c := newChunk()
	return &Queue{ceiling: n, head: c, tail: c}
}

// Push copies frame into the queue and reports the oldest frames it
// dropped to make room for it. It refuses a frame the queue cannot
// hold, one that is empty, larger than the whole ceiling, or larger
// than a length prefix can describe.
func (q *Queue) Push(frame []byte) (EvictedFrames, bool) {
	n := len(frame)
	if n == 0 || n > q.ceiling || n > maxFrameLen {
		return EvictedFrames{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	// A frame within the ceiling always fits an empty queue,
	// so this cannot run out of frames to drop.
	var evicted EvictedFrames
	for q.frameSize+n > q.ceiling {
		evicted.Bytes += q.evictLocked()
		evicted.Count++
	}
	q.writeHeaderLocked(uint32(n))
	q.writeLocked(frame)
	q.frameSize += n
	q.frameCount++

	return evicted, true
}

// Pop appends the oldest frame to dst and returns the grown slice
// with the length of that frame. It appends nothing, and reports a
// length of zero, when the queue is empty or when the oldest frame
// is larger than the given limit.
//
// A limit of zero admits a frame of any size, which is how a caller
// pops one larger than the batch it is filling.
func (q *Queue) Pop(dst []byte, limit int) ([]byte, int) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.frameCount == 0 {
		return dst, 0
	}
	n := q.nextLenLocked()
	if limit > 0 && n > limit {
		return dst, 0
	}
	q.head.r += frameHeaderLen

	for left := n; left > 0; {
		b := q.readLocked(left)
		dst = append(dst, b...)
		left -= len(b)
	}
	q.frameSize -= n
	q.frameCount--
	if q.frameCount == 0 {
		q.rewindLocked()
	}
	return dst, n
}

// Usage returns the frame bytes the queue is holding.
func (q *Queue) Usage() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.frameSize
}

// Len returns the number of frames the queue is holding.
func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.frameCount
}

// writeHeaderLocked writes the length prefix of a frame of n bytes.
// It moves to a new chunk when too few bytes are left to hold the
// prefix whole, which can leave the tail of a chunk unused.
func (q *Queue) writeHeaderLocked(n uint32) {
	if q.tail.free() < frameHeaderLen {
		q.growLocked()
	}
	binary.LittleEndian.PutUint32(q.tail.buf[q.tail.w:], n)
	q.tail.w += frameHeaderLen
}

// writeLocked copies b into the tail of the chain, taking a new chunk
// from the pool each time it fills up the one it is writing to.
func (q *Queue) writeLocked(b []byte) {
	for len(b) > 0 {
		if q.tail.free() == 0 {
			q.growLocked()
		}
		n := copy(q.tail.buf[q.tail.w:], b)
		q.tail.w += n
		b = b[n:]
	}
}

// skipDrainedLocked advances the head past the chunks the reader
// has drained, releasing each of them to the pool. The last chunk
// is never released, so a queue always holds at least one.
func (q *Queue) skipDrainedLocked() {
	for q.head.unread() == 0 && q.head.next != nil {
		next := q.head.next
		q.head.release()
		q.head = next
	}
}

// nextLenLocked returns the length of the oldest frame, without
// consuming it.
func (q *Queue) nextLenLocked() int {
	q.skipDrainedLocked()
	return int(binary.LittleEndian.Uint32(q.head.buf[q.head.r:]))
}

// readLocked returns up to n bytes of the oldest frame and advances
// past them. It stops at the end of a chunk, so a frame spanning
// several chunks is read by calling it until n bytes are consumed.
//
// The bytes belong to the chunk, and outlive neither the frame nor
// the lock.
func (q *Queue) readLocked(n int) []byte {
	q.skipDrainedLocked()
	n = min(n, q.head.unread())
	b := q.head.buf[q.head.r : q.head.r+n]
	q.head.r += n
	return b
}

// rewindLocked resets the single chunk an empty queue keeps, so that
// it is written from its start again. Both offsets only ever move
// forward, so without it a sink holding a single frame at a time would
// walk its chunk to the end and take a fresh one from the pool, over
// and over, even though the queue never holds more than a single frame.
//
// It is sound only once the last frame is gone, where nothing is left
// unread and the queue holds no other chunk.
func (q *Queue) rewindLocked() {
	q.head.r = 0
	q.head.w = 0
}

// evictLocked drops the oldest frame and returns the bytes it freed.
func (q *Queue) evictLocked() int {
	n := q.nextLenLocked()
	q.head.r += frameHeaderLen

	for left := n; left > 0; {
		left -= len(q.readLocked(left))
	}
	q.frameSize -= n
	q.frameCount--
	if q.frameCount == 0 {
		q.rewindLocked()
	}
	return n
}

// growLocked links a fresh chunk from the pool as the new tail.
func (q *Queue) growLocked() {
	c := newChunk()
	q.tail.next = c
	q.tail = c
}
