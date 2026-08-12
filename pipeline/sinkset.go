package pipeline

const (
	sinkSetWordBits = 64
)

// sinkSet records which sinks an event has reached.
// A sink is a single instance shared by every pipeline naming
// it, so an event matching two such pipelines reaches it once.
type sinkSet []uint64

// newSinkSet returns a set covering n sinks.
func newSinkSet(n int) sinkSet {
	return make(sinkSet, (n+sinkSetWordBits-1)/sinkSetWordBits)
}

// mark records that an event reached the sink at index i,
// and reports whether it is the first one.
func (s sinkSet) mark(i int) bool {
	word, bit := i/sinkSetWordBits, uint64(1)<<(i%sinkSetWordBits)
	if s[word]&bit != 0 {
		return false
	}
	s[word] |= bit
	return true
}

// clear empties the set.
func (s sinkSet) clear() {
	clear(s)
}
