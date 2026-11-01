package ai

import (
	"context"
	"math"
	"slices"
	"strings"
	"testing"
)

func dot(a, b []float32) float64 {
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

func TestFakeEmbed(t *testing.T) {
	ctx := context.Background()
	f := Fake{}

	a, err := f.Embed(ctx, "Database migration failed on billing")
	if err != nil {
		t.Fatal(err)
	}
	again, _ := f.Embed(ctx, "database MIGRATION failed, on billing!")
	if !slices.Equal(a, again) {
		t.Fatal("Embed isn't deterministic across case and punctuation")
	}
	if len(a) != Dims {
		t.Fatalf("got %d dims, want %d", len(a), Dims)
	}
	if n := math.Sqrt(dot(a, a)); math.Abs(n-1) > 1e-6 {
		t.Fatalf("norm = %v, want 1", n)
	}

	near, _ := f.Embed(ctx, "billing migration")
	far, _ := f.Embed(ctx, "team lunch on friday")
	if dot(a, near) <= dot(a, far) {
		t.Fatalf("shared words should score higher: near %v, far %v", dot(a, near), dot(a, far))
	}

	zero, err := f.Embed(ctx, " ... ")
	if err != nil || len(zero) != Dims || dot(zero, zero) != 0 {
		t.Fatalf("text with no words = %v, %v; want the zero vector", zero[:3], err)
	}
}

func TestFakeSummarize(t *testing.T) {
	ctx := context.Background()
	thread := "Alice Chen: " + strings.Repeat("word ", 100)
	s1, usage, err := Fake{}.Summarize(ctx, thread)
	if err != nil {
		t.Fatal(err)
	}
	s2, _, _ := Fake{}.Summarize(ctx, thread)
	if s1 != s2 || !strings.HasPrefix(s1, "Summary: Alice Chen: word") {
		t.Fatalf("summary = %q, %q", s1, s2)
	}
	if got := len(strings.Fields(s1)); got != fakeSummaryWords+1 {
		t.Fatalf("summary has %d words, want %d", got, fakeSummaryWords+1)
	}
	if usage.InputTokens != 102 || usage.OutputTokens != fakeSummaryWords+1 {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestFakeHonoursTheContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (Fake{}).Summarize(ctx, "A: b"); err == nil {
		t.Error("Summarize with a cancelled context returned nil error")
	}
	if _, err := (Fake{}).Embed(ctx, "x"); err == nil {
		t.Error("Embed with a cancelled context returned nil error")
	}
}
