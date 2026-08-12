package buffer

// Shrink releases a buffer that grew far past what it is using, so that
// a single outsized payload cannot pin its capacity for the life of the
// process. One still using most of what it holds is left alone, so a
// workload that is simply large never pays to grow its buffer back.
//
// A caller truncates its buffer before filling it again, and Shrink has
// to run ahead of that, while the length still shows how much of the
// capacity the last use needed.
func Shrink(b []byte, threshold int) []byte {
	if cap(b) > threshold && len(b) < cap(b)/4 {
		return nil
	}
	return b
}
