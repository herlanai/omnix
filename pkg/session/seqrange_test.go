package session

import (
	"reflect"
	"testing"
)

func TestEncodeSeqRanges(t *testing.T) {
	// Three consecutive values form a range; isolated values and pairs remain singletons.
	got := EncodeSeqRanges([]int{1, 2, 3, 5, 7, 8})
	want := []SeqRange{{1, 3}, {5, 5}, {7, 7}, {8, 8}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got = %v want = %v", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	src := []int{2, 3, 4, 5, 9}
	dec, err := DecodeSeqRanges(EncodeSeqRanges(src), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dec, src) {
		t.Fatalf("round trip = %v", dec)
	}
}

func TestDecodeBadRange(t *testing.T) {
	if _, err := DecodeSeqRanges([]SeqRange{{5, 3}}, 0); err == nil {
		t.Fatal("inverted range must error")
	}
}

func TestDecodeMaxEntries(t *testing.T) {
	if _, err := DecodeSeqRanges([]SeqRange{{1, 5}}, 3); err == nil {
		t.Fatal("exceeding max entries must error")
	}
}

func TestDecodeIntegerBoundaries(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name    string
		ranges  []SeqRange
		limit   int
		want    []int
		wantErr bool
	}{
		{"maximum singleton", []SeqRange{{maxInt, maxInt}}, 0, []int{maxInt}, false},
		{"maximum endpoint", []SeqRange{{maxInt - 1, maxInt}}, 2, []int{maxInt - 1, maxInt}, false},
		{"overflow", []SeqRange{{0, maxInt}}, 0, nil, true},
		{"cumulative overflow", []SeqRange{{0, maxInt - 1}, {maxInt, maxInt}}, 0, nil, true},
		{"reject huge range before expansion", []SeqRange{{0, maxInt}}, 10, nil, true},
		{"unordered singletons", []SeqRange{{2, 2}, {1, 1}, {1, 1}}, 0, []int{2, 1, 1}, false},
		{"overlapping ranges", []SeqRange{{1, 3}, {3, 4}}, 0, nil, true},
		{"empty", nil, 1, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeSeqRanges(tt.ranges, tt.limit)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
