package ai

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

// FakeModel is the model name Fake reports.
const FakeModel = "fake"

const fakeSummaryWords = 40

// Fake is a deterministic Summarizer and Embedder with no network, for tests and
// AI_PROVIDER=fake. Texts that share words get similar vectors, so ranking tests
// behave like the real model in miniature.
type Fake struct{}

// Summarize returns the first words of the thread.
func (Fake) Summarize(ctx context.Context, thread string) (string, Usage, error) {
	if err := ctx.Err(); err != nil {
		return "", Usage{}, err
	}
	words := strings.Fields(thread)
	kept := words[:min(len(words), fakeSummaryWords)]
	summary := "Summary: " + strings.Join(kept, " ")
	return summary, Usage{InputTokens: len(words), OutputTokens: len(kept) + 1}, nil
}

// Embed hashes each lowercased word into one of Dims buckets, then normalizes.
// Text with no words gives the zero vector.
func (Fake) Embed(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vec := make([]float32, Dims)
	for word := range strings.FieldsFuncSeq(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(word))
		vec[h.Sum32()%Dims]++
	}
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum > 0 {
		n := float32(math.Sqrt(sum))
		for i := range vec {
			vec[i] /= n
		}
	}
	return vec, nil
}
