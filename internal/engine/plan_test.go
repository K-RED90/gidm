package engine

import (
	"testing"
)

func TestPlanSegments(t *testing.T) {
	tests := []struct {
		name      string
		totalSize int64
		n         int
		want      []Segment
	}{
		{
			name:      "even split",
			totalSize: 4 << 20, // 4 MiB into 4
			n:         4,
			want: []Segment{
				{Index: 0, Start: 0, End: 1<<20 - 1},
				{Index: 1, Start: 1 << 20, End: 2<<20 - 1},
				{Index: 2, Start: 2 << 20, End: 3<<20 - 1},
				{Index: 3, Start: 3 << 20, End: 4<<20 - 1},
			},
		},
		{
			name:      "uneven split takes remainder in last",
			totalSize: 4<<20 + 3, // 3 trailing bytes
			n:         4,
			want: []Segment{
				{Index: 0, Start: 0, End: 1<<20 - 1},
				{Index: 1, Start: 1 << 20, End: 2<<20 - 1},
				{Index: 2, Start: 2 << 20, End: 3<<20 - 1},
				{Index: 3, Start: 3 << 20, End: 4<<20 + 3 - 1},
			},
		},
		{
			name:      "n=1 is one full-range segment",
			totalSize: 10 << 20,
			n:         1,
			want:      []Segment{{Index: 0, Start: 0, End: 10<<20 - 1}},
		},
		{
			name:      "tiny file under floor stays single",
			totalSize: 500,
			n:         8,
			want:      []Segment{{Index: 0, Start: 0, End: 499}},
		},
		{
			name:      "size just over floor allows two but floor caps to one each",
			totalSize: minSegmentSize + 10,
			n:         8,
			want:      []Segment{{Index: 0, Start: 0, End: minSegmentSize + 10 - 1}},
		},
		{
			name:      "exactly two floors yields two segments",
			totalSize: 2 * minSegmentSize,
			n:         8,
			want: []Segment{
				{Index: 0, Start: 0, End: minSegmentSize - 1},
				{Index: 1, Start: minSegmentSize, End: 2*minSegmentSize - 1},
			},
		},
		{
			name:      "unknown size yields single open-ended segment",
			totalSize: -1,
			n:         8,
			want:      []Segment{{Index: 0, Start: 0, End: -1}},
		},
		{
			name:      "zero size yields single open-ended segment",
			totalSize: 0,
			n:         8,
			want:      []Segment{{Index: 0, Start: 0, End: -1}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planSegments(tt.totalSize, tt.n)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d (%+v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("segment[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// Segments must exactly tile [0, totalSize) with no gaps or overlaps for any
// known size and count.
func TestPlanSegmentsTilesExactly(t *testing.T) {
	sizes := []int64{minSegmentSize, 3*minSegmentSize + 7, 10 << 20, 100 << 20}
	counts := []int{1, 2, 3, 5, 8, 16}
	for _, size := range sizes {
		for _, n := range counts {
			segs := planSegments(size, n)
			if segs[0].Start != 0 {
				t.Errorf("size=%d n=%d: first start = %d, want 0", size, n, segs[0].Start)
			}
			if last := segs[len(segs)-1]; last.End != size-1 {
				t.Errorf("size=%d n=%d: last end = %d, want %d", size, n, last.End, size-1)
			}
			var total int64
			for i, s := range segs {
				if i > 0 && s.Start != segs[i-1].End+1 {
					t.Errorf("size=%d n=%d: gap/overlap at %d", size, n, i)
				}
				total += s.Size()
			}
			if total != size {
				t.Errorf("size=%d n=%d: total = %d, want %d", size, n, total, size)
			}
		}
	}
}
