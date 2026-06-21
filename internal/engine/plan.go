package engine

const (
	// defaultBufferSize backs the transfer buffer when config supplies none.
	defaultBufferSize = 64 * 1024

	// minSegmentSize is the floor below which splitting buys nothing: extra
	// connections and request overhead would dominate the bytes moved. Files at
	// or under it download as a single segment.
	minSegmentSize = 1 << 20 // 1 MiB
)

// planSegments splits totalSize into n inclusive [Start,End] byte ranges of
// ~equal size, the last segment absorbing the remainder. It returns a single
// full-range segment when the size is unknown (totalSize <= 0), when n <= 1, or
// when the file is small enough that splitting it would create sub-floor
// segments. A planned segment's Completed is always zero; resume state is layered
// on by the caller.
func planSegments(totalSize int64, n int) []Segment {
	if totalSize <= 0 {
		// Unknown size: one open-ended segment fetched whole-body.
		return []Segment{{Index: 0, Start: 0, End: -1}}
	}
	if n < 1 {
		n = 1
	}
	// Never carve segments below the floor; cap the count so each holds at least
	// minSegmentSize (the last one may hold more via the remainder).
	if max := totalSize / minSegmentSize; max < int64(n) {
		n = int(max)
	}
	if n < 1 {
		n = 1
	}

	segs := make([]Segment, n)
	base := totalSize / int64(n)
	start := int64(0)
	for i := range n {
		end := start + base - 1
		if i == n-1 {
			end = totalSize - 1 // last segment takes the remainder
		}
		segs[i] = Segment{Index: i, Start: start, End: end}
		start = end + 1
	}
	return segs
}
