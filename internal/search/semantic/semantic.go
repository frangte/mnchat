// Package semantic ranks summarized threads by cosine similarity in Go, because
// MySQL Community has no DISTANCE() or VECTOR_DISTANCE().
package semantic

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"slices"
)

// Candidate is one summarized thread with its embedding as the VECTOR column
// stores it: 4 bytes per dimension, little-endian float32.
type Candidate struct {
	ThreadRootID int64
	Embedding    []byte
}

type Hit struct {
	ThreadRootID int64
	Score        float64
}

// Decode reads a VECTOR column value.
func Decode(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("vector has %d bytes, not a multiple of 4", len(b))
	}
	vec := make([]float32, len(b)/4)
	for i := range vec {
		vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return vec, nil
}

// cosine returns the cosine similarity of two vectors of the same length, given
// the norm of a. A zero vector has no direction, so it scores 0 rather than NaN.
func cosine(a, b []float32, normA float64) float64 {
	var dot, sumB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		sumB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || sumB == 0 {
		return 0
	}
	return dot / (normA * math.Sqrt(sumB))
}

func norm(v []float32) float64 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	return math.Sqrt(sum)
}

// Rank scores every candidate against query and returns the best limit hits, best
// first. There is no score cutoff: matching and non-matching scores overlap, so
// the caller shows the top few. Equal scores put the newer thread first, the same
// tie-break as text search.
//
// A candidate that can't be compared, such as one stored by a model with another
// dimension, is logged and skipped, so one bad row can't fail every search.
func Rank(query []float32, candidates []Candidate, limit int) []Hit {
	queryNorm := norm(query)
	hits := make([]Hit, 0, len(candidates))
	for _, c := range candidates {
		vec, err := Decode(c.Embedding)
		if err == nil && len(vec) != len(query) {
			err = fmt.Errorf("vector has %d dimensions, query has %d", len(vec), len(query))
		}
		if err != nil {
			slog.Warn("skipping thread vector", "thread_root_id", c.ThreadRootID, "err", err)
			continue
		}
		score := cosine(query, vec, queryNorm)
		// Only a corrupt vector holding NaN or Inf gets here, and NaN would break the sort.
		if math.IsNaN(score) || math.IsInf(score, 0) {
			slog.Warn("skipping thread vector", "thread_root_id", c.ThreadRootID, "err", "score is not finite")
			continue
		}
		hits = append(hits, Hit{ThreadRootID: c.ThreadRootID, Score: score})
	}

	slices.SortFunc(hits, func(a, b Hit) int {
		if c := cmp.Compare(b.Score, a.Score); c != 0 {
			return c
		}
		return cmp.Compare(b.ThreadRootID, a.ThreadRootID)
	})
	if limit >= 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}
