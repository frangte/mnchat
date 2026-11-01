package ai

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	opSummarize = "summarize"
	opEmbed     = "embed"
)

// Metrics holds the model call collectors. Names are a contract with the
// dashboards; see docs/tasks/CONTRACTS.md.
type Metrics struct {
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	tokens   *prometheus.CounterVec
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	m := &Metrics{
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_ai_requests_total",
			Help: "Model calls, by operation and outcome.",
		}, []string{"op", "outcome"}),
		duration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name: "mnchat_ai_duration_seconds",
			Help: "Model call latency, including a cold model load.",
			// A warm embedding takes about 20 ms, a cold one 1-2 s, and a summary
			// 2-5 s. The 2 s and 60 s boundaries are chat's deadlines.
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2, 3, 5, 10, 20, 30, 60},
		}, []string{"op"}),
		tokens: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "mnchat_ai_tokens_total",
			Help: "Chat model tokens for summaries, from prompt_eval_count and eval_count. Embeddings aren't counted.",
		}, []string{"direction"}),
	}
	// Export every series from zero, so rate() sees the first call and first failure.
	for _, op := range []string{opSummarize, opEmbed} {
		m.duration.WithLabelValues(op)
		for _, outcome := range []string{"ok", "error"} {
			m.requests.WithLabelValues(op, outcome)
		}
	}
	for _, direction := range []string{"input", "output"} {
		m.tokens.WithLabelValues(direction)
	}
	return m
}

func (m *Metrics) observe(op string, start time.Time, err error) {
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	m.requests.WithLabelValues(op, outcome).Inc()
	m.duration.WithLabelValues(op).Observe(time.Since(start).Seconds())
}

func (m *Metrics) addTokens(u Usage) {
	m.tokens.WithLabelValues("input").Add(float64(u.InputTokens))
	m.tokens.WithLabelValues("output").Add(float64(u.OutputTokens))
}
