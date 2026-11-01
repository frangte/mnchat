package semantic

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"
)

func encode(vec ...float32) []byte {
	b := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(v))
	}
	return b
}

func TestDecode(t *testing.T) {
	// The stored form MySQL reports for STRING_TO_VECTOR('[1,0,0,0]').
	got, err := Decode([]byte{0x00, 0x00, 0x80, 0x3f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []float32{1, 0, 0, 0}) {
		t.Fatalf("Decode = %v, want [1 0 0 0]", got)
	}

	want := []float32{-2.5, 0.1, math.MaxFloat32, 1.0 / 3}
	got, err = Decode(encode(want...))
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Decode = %v, %v; want %v", got, err, want)
	}

	if got, err := Decode(nil); err != nil || len(got) != 0 {
		t.Fatalf("Decode(nil) = %v, %v; want an empty vector", got, err)
	}
	if _, err := Decode(make([]byte, 7)); err == nil {
		t.Fatal("Decode of 7 bytes returned nil error")
	}
}

func TestCosine(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
	}{
		{name: "same direction", a: []float32{1, 2, 3}, b: []float32{2, 4, 6}, want: 1},
		{name: "opposite", a: []float32{1, 0}, b: []float32{-3, 0}, want: -1},
		{name: "orthogonal", a: []float32{1, 0}, b: []float32{0, 5}, want: 0},
		{name: "45 degrees", a: []float32{1, 0}, b: []float32{1, 1}, want: 1 / math.Sqrt2},
		{name: "zero query", a: []float32{0, 0}, b: []float32{1, 1}, want: 0},
		{name: "zero candidate", a: []float32{1, 1}, b: []float32{0, 0}, want: 0},
		{name: "both zero", a: []float32{0, 0}, b: []float32{0, 0}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosine(tt.a, tt.b, norm(tt.a))
			if math.IsNaN(got) || math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("cosine = %v, want %v", got, tt.want)
			}
		})
	}
}

func ids(hits []Hit) []int64 {
	out := make([]int64, len(hits))
	for i, h := range hits {
		out[i] = h.ThreadRootID
	}
	return out
}

func TestRank(t *testing.T) {
	query := []float32{1, 0, 0}
	candidates := []Candidate{
		{ThreadRootID: 10, Embedding: encode(0, 1, 0)},     // orthogonal, 0
		{ThreadRootID: 11, Embedding: encode(1, 1, 0)},     // 0.707
		{ThreadRootID: 12, Embedding: encode(2, 0, 0)},     // 1
		{ThreadRootID: 13, Embedding: encode(0, 0, 0)},     // zero vector, 0
		{ThreadRootID: 14, Embedding: encode(-1, 0, 0)},    // -1
		{ThreadRootID: 15, Embedding: encode(1, 1, 0)},     // ties with 11
		{ThreadRootID: 16, Embedding: encode(1, 0)},        // wrong dimension, skipped
		{ThreadRootID: 17, Embedding: []byte{1, 2, 3}},     // not float32s, skipped
		{ThreadRootID: 18, Embedding: encode(nan(), 0, 0)}, // corrupt, skipped
	}

	hits := Rank(query, candidates, 20)
	if want := []int64{12, 15, 11, 13, 10, 14}; !slices.Equal(ids(hits), want) {
		t.Fatalf("order = %v, want %v", ids(hits), want)
	}
	for _, h := range hits {
		if math.IsNaN(h.Score) {
			t.Fatalf("thread %d scored NaN", h.ThreadRootID)
		}
	}
	if hits[0].Score != 1 || hits[3].Score != 0 || hits[5].Score != -1 {
		t.Fatalf("scores = %+v", hits)
	}

	if got := ids(Rank(query, candidates, 2)); !slices.Equal(got, []int64{12, 15}) {
		t.Fatalf("limit 2 = %v, want the best two", got)
	}
	if got := Rank(query, nil, 5); len(got) != 0 {
		t.Fatalf("no candidates = %v, want none", got)
	}
}

func TestRankWithZeroQueryKeepsAStableOrder(t *testing.T) {
	candidates := []Candidate{
		{ThreadRootID: 3, Embedding: encode(1, 0)},
		{ThreadRootID: 9, Embedding: encode(0, 1)},
		{ThreadRootID: 5, Embedding: encode(1, 1)},
	}
	hits := Rank([]float32{0, 0}, candidates, 5)
	if want := []int64{9, 5, 3}; !slices.Equal(ids(hits), want) {
		t.Fatalf("order = %v, want newest first on equal scores", ids(hits))
	}
	for _, h := range hits {
		if h.Score != 0 {
			t.Fatalf("zero query scored %v, want 0", h.Score)
		}
	}
}

func nan() float32 { return float32(math.NaN()) }
