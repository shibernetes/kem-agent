package pipeline

import (
	"bytes"
	"sync"
	"testing"

	"github.com/shibernetes/kem-agent/sink"
)

func TestQueueRoundTripsInOrder(t *testing.T) {
	q := NewQueue(1 << 20)
	want := [][]byte{frame(0, 1), frame(1, 300), frame(2, 7), frame(3, 4096)}

	size := 0
	for _, f := range want {
		mustPush(t, q, f)
		size += len(f)
	}
	if got := q.Len(); got != len(want) {
		t.Errorf("got %d frames, want %d", got, len(want))
	}
	if got := q.Usage(); got != size {
		t.Errorf("got %d bytes in use, want %d", got, size)
	}
	wantFrames(t, q, want...)

	if got := q.Usage(); got != 0 {
		t.Errorf("got %d bytes in use once drained, want none", got)
	}
}

func TestQueueSpansChunks(t *testing.T) {
	q := NewQueue(1 << 20)

	// Three chunks and a remainder, so the frame is cut in four.
	wantFrames(t, q, mustPush(t, q, frame(1, 3*sink.QueueChunkSize+11)))
}

func TestQueuePadsChunkTail(t *testing.T) {
	q := NewQueue(1 << 20)

	// Fill the chunk to two bytes short of its end, which is too
	// few to hold the next frame's prefix.
	first := mustPush(t, q, frame(1, sink.QueueChunkSize-frameHeaderLen-2))
	if got := q.tail.free(); got != 2 {
		t.Fatalf("got %d bytes free, want 2", got)
	}
	second := mustPush(t, q, frame(2, 8))
	if q.head == q.tail {
		t.Error("got one chunk, want the prefix moved past the unused bytes")
	}
	wantFrames(t, q, first, second)
}

func TestQueueRefusesUnholdableFrames(t *testing.T) {
	const ceiling = 4 << 10
	cases := map[string]struct {
		size     int
		wantHeld bool
	}{
		"empty":             {0, false},
		"over the ceiling":  {ceiling + 1, false},
		"at the ceiling":    {ceiling, true},
		"under the ceiling": {ceiling - 1, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := NewQueue(ceiling)
			_, ok := q.Push(frame(1, tc.size))
			switch {
			case tc.wantHeld && !ok:
				t.Error("got a refused frame, want it accepted")
			case !tc.wantHeld && ok:
				t.Error("got an accepted frame, want it refused")
			case tc.wantHeld && q.Len() != 1:
				t.Errorf("got %d frames, want the one accepted", q.Len())
			case !tc.wantHeld && q.Len() != 0:
				t.Errorf("got %d frames, want a refusal to leave none", q.Len())
			}
		})
	}
}

func TestQueueEvictsOldest(t *testing.T) {
	q := NewQueue(sink.QueueChunkSize)
	const size = 1000
	fits := sink.QueueChunkSize / size

	for i := range fits {
		if evicted := mustPushN(t, q, frame(i, size)); evicted.Count != 0 {
			t.Fatalf("got %d frame evictions, want none", evicted.Count)
		}
	}
	evicted := mustPushN(t, q, frame(fits, size))
	if evicted.Count != 1 {
		t.Errorf("got %d evictions on the frame that overflowed, want 1", evicted.Count)
	}
	if evicted.Bytes != size {
		t.Errorf("got %d bytes evicted, want %d", evicted.Bytes, size)
	}
	if got := q.Usage(); got != fits*size {
		t.Errorf("got %d bytes in use, want %d under a ceiling of %d",
			got, fits*size, sink.QueueChunkSize)
	}
	// The oldest frame was evicted, and the rest kept their order.
	want := make([][]byte, fits)
	for i := range want {
		want[i] = frame(i+1, size)
	}
	wantFrames(t, q, want...)
}

func TestQueueEvictsEverything(t *testing.T) {
	const ceiling = 4 << 10

	q := NewQueue(ceiling)
	mustPush(t, q, frame(1, 100))
	mustPush(t, q, frame(2, 100))

	// A frame filling the whole ceiling leaves room for nothing else.
	var (
		whole   = frame(3, ceiling)
		evicted = mustPushN(t, q, whole)
	)
	if evicted.Count != 2 {
		t.Errorf("got %d evictions, want 2", evicted.Count)
	}
	if evicted.Bytes != 200 {
		t.Errorf("got %d bytes evicted, want 200", evicted.Bytes)
	}
	// Emptying the queue rewound its chunk, so the frame that emptied
	// it was written from the start rather than after what it evicted.
	if q.head.r != 0 {
		t.Errorf("got read offset %d, want the chunk rewound before the write", q.head.r)
	}
	wantFrames(t, q, whole)
}

func TestQueuePopLimit(t *testing.T) {
	const size = 500
	cases := map[string]struct {
		limit    int
		wantRead int
		wantLeft int
	}{
		"no limit":       {0, size, 0},
		"room to spare":  {size + 1, size, 0},
		"exactly enough": {size, size, 0},
		"one byte short": {size - 1, 0, 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q := NewQueue(1 << 20)
			mustPush(t, q, frame(1, size))

			if _, got := q.Pop(nil, tc.limit); got != tc.wantRead {
				t.Errorf("got %d bytes, want %d", got, tc.wantRead)
			}
			// A frame that did not fit waits for the next batch.
			if got := q.Len(); got != tc.wantLeft {
				t.Errorf("got %d frames left, want %d", got, tc.wantLeft)
			}
		})
	}
}

func TestQueueRewindsWhenEmpty(t *testing.T) {
	q := NewQueue(1 << 20)
	f := frame(1, 512)

	// One frame in and one out, far more often than a chunk holds.
	for range 4 * sink.QueueChunkSize / len(f) {
		mustPush(t, q, f)
		if _, got := q.Pop(nil, 0); got != len(f) {
			t.Fatalf("got %d bytes, want %d", got, len(f))
		}
	}
	if q.head != q.tail {
		t.Error("got a chain, want the queue reusing the chunk it keeps")
	}
	if q.head.r != 0 || q.head.w != 0 {
		t.Errorf("got offsets r=%d w=%d, want the chunk rewound", q.head.r, q.head.w)
	}
}

func TestQueueConcurrentProducers(t *testing.T) {
	const writers, each = 8, 250

	// Wide enough that nothing is evicted, so the count is exact.
	q := NewQueue(64 << 20)

	// Every byte of a writer's frame carries that writer, so a
	// frame torn between two of them reads as mixed content.
	want := make([][]byte, writers)
	for id := range want {
		want[id] = bytes.Repeat([]byte{byte(id)}, 1+id*64)
	}
	var wg sync.WaitGroup
	for id := range writers {
		wg.Go(func() {
			for range each {
				if _, ok := q.Push(want[id]); !ok {
					t.Errorf("got a refused push from writer %d", id)
					return
				}
			}
		})
	}
	wg.Wait()

	counts := make(map[int]int)
	for _, got := range popAll(q) {
		id := int(got[0])
		if id >= writers || !bytes.Equal(got, want[id]) {
			t.Fatalf("got a %d byte frame tagged %d, want one writer's frame whole", len(got), id)
		}
		counts[id]++
	}
	if len(counts) != writers {
		t.Errorf("got frames from %d writers, want %d", len(counts), writers)
	}
	for id, got := range counts {
		if got != each {
			t.Errorf("got %d frames from writer %d, want %d", got, id, each)
		}
	}
}

// frame returns an n byte frame whose content varies with i and
// with the position within it, so a misplaced copy reads as wrong
// content rather than as a pattern that happens to match.
func frame(i, n int) []byte {
	b := make([]byte, n)
	for j := range b {
		b[j] = byte(i*31 + j)
	}
	return b
}

// mustPush pushes b to the queue and returns it, for a caller
// that only needs the frame back to compare against it later.
func mustPush(t *testing.T, q *Queue, b []byte) []byte {
	t.Helper()

	mustPushN(t, q, b)
	return b
}

// mustPushN pushes a frame and returns what has evicted.
func mustPushN(t *testing.T, q *Queue, b []byte) EvictedFrames {
	t.Helper()

	evicted, ok := q.Push(b)
	if !ok {
		t.Fatalf("push of %d bytes refused", len(b))
	}
	return evicted
}

// popAll pops every frame the queue holds, reusing one buffer,
// and returns them in the order they came out.
func popAll(q *Queue) [][]byte {
	var (
		out [][]byte
		buf []byte
	)
	for {
		var n int
		if buf, n = q.Pop(buf[:0], 0); n == 0 {
			return out
		}
		out = append(out, bytes.Clone(buf))
	}
}

// wantFrames fails unless the queue drains to exactly the given
// frames, in exact order.
func wantFrames(t *testing.T, q *Queue, want ...[]byte) {
	t.Helper()

	got := popAll(q)
	if len(got) != len(want) {
		t.Fatalf("got %d frames, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("frame %d came back changed, got %d bytes, want %d",
				i, len(got[i]), len(want[i]))
		}
	}
}
