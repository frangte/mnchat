package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// keepAlive keeps both models loaded between requests. A cold summary model
	// adds about 4 s, and a cold embedding model 1-2 s, which is most of chat's
	// 2 s SearchThreads deadline.
	keepAlive = "30m"

	temperature = 0.2
	// numPredict caps a summary at 300 tokens, far more than 2-3 sentences need.
	numPredict = 300

	// maxResponseBytes bounds what a response may hold. A 1024-dim embedding is
	// about 20 KB of JSON.
	maxResponseBytes = 1 << 20

	warmUpInput = "warm up"
)

type OllamaConfig struct {
	URL        string // such as http://localhost:11434
	ChatModel  string
	EmbedModel string
}

// Ollama calls a local Ollama server. It implements Summarizer and Embedder.
type Ollama struct {
	baseURL    string
	chatModel  string
	embedModel string
	client     *http.Client
	metrics    *Metrics
}

// NewOllama fails only on a malformed URL. An unreachable Ollama is reported per
// request, so search starts, and serves text search, without it.
func NewOllama(cfg OllamaConfig, m *Metrics) (*Ollama, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("ollama url %q: want http://host:port", cfg.URL)
	}
	return &Ollama{
		baseURL:    strings.TrimRight(cfg.URL, "/"),
		chatModel:  cfg.ChatModel,
		embedModel: cfg.EmbedModel,
		// No client timeout: every call runs under the request context, which the
		// server bounds, so a caller's deadline cancels the model call too.
		client:  &http.Client{},
		metrics: m,
	}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
	NumPredict  int     `json:"num_predict"`
}

type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []chatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	KeepAlive string        `json:"keep_alive"`
	Options   chatOptions   `json:"options"`
}

type chatResponse struct {
	Message         chatMessage `json:"message"`
	DoneReason      string      `json:"done_reason"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

// threadTag matches anything that could open or close the <thread> wrapper, so a
// message can't end the data section early and pose as instructions.
var threadTag = regexp.MustCompile(`(?i)<\s*/?\s*thread\s*>`)

func (o *Ollama) Summarize(ctx context.Context, thread string) (string, Usage, error) {
	start := time.Now()
	summary, usage, err := o.summarize(ctx, thread)
	o.metrics.observe(opSummarize, start, err)
	o.metrics.addTokens(usage)
	return summary, usage, err
}

func (o *Ollama) summarize(ctx context.Context, thread string) (string, Usage, error) {
	req := chatRequest{
		Model: o.chatModel,
		Messages: []chatMessage{
			{Role: "system", Content: SystemPrompt},
			{Role: "user", Content: "<thread>\n" + threadTag.ReplaceAllString(thread, "[thread]") + "\n</thread>"},
		},
		KeepAlive: keepAlive,
		Options:   chatOptions{Temperature: temperature, NumPredict: numPredict},
	}
	var resp chatResponse
	if err := o.post(ctx, "/api/chat", req, &resp); err != nil {
		return "", Usage{}, err
	}
	usage := Usage{InputTokens: resp.PromptEvalCount, OutputTokens: resp.EvalCount}

	summary := strings.TrimSpace(resp.Message.Content)
	// "length" means the model stopped at numPredict mid-sentence. Keep the
	// complete sentences rather than fail, because a retry at this temperature
	// would most likely stop in the same place.
	if resp.DoneReason == "length" {
		cut := completeSentences(summary)
		if cut == "" {
			return "", usage, ErrTruncated
		}
		slog.Warn("summary hit the token limit, keeping its complete sentences",
			"tokens", resp.EvalCount, "kept_chars", len(cut), "chars", len(summary))
		summary = cut
	}
	if summary == "" {
		return "", usage, errors.New("model returned an empty summary")
	}
	return summary, usage, nil
}

// completeSentences drops a trailing unfinished sentence. A sentence ends at '.',
// '!' or '?' followed by a space or the end of the text, so "3.5" doesn't end one.
func completeSentences(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		switch s[i] {
		case '.', '!', '?':
			if i == len(s)-1 || s[i+1] == ' ' || s[i+1] == '\n' {
				return s[:i+1]
			}
		}
	}
	return ""
}

type embedRequest struct {
	Model     string `json:"model"`
	Input     string `json:"input"`
	KeepAlive string `json:"keep_alive"`
}

type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (o *Ollama) Embed(ctx context.Context, text string) ([]float32, error) {
	start := time.Now()
	vec, err := o.embed(ctx, text)
	o.metrics.observe(opEmbed, start, err)
	return vec, err
}

// WarmUp loads the embedding model without counting a request, so a restart
// doesn't show up as traffic or, with Ollama down, as a failure.
func (o *Ollama) WarmUp(ctx context.Context) error {
	_, err := o.embed(ctx, warmUpInput)
	return err
}

func (o *Ollama) embed(ctx context.Context, text string) ([]float32, error) {
	var resp embedResponse
	err := o.post(ctx, "/api/embed", embedRequest{Model: o.embedModel, Input: text, KeepAlive: keepAlive}, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) != 1 {
		return nil, fmt.Errorf("ollama returned %d embeddings, want 1", len(resp.Embeddings))
	}
	// Another model, or another size, would store vectors that can't be compared.
	if got := len(resp.Embeddings[0]); got != Dims {
		return nil, fmt.Errorf("embedding has %d dimensions, want %d", got, Dims)
	}
	return resp.Embeddings[0], nil
}

func (o *Ollama) post(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("ollama %s: read response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		// Ollama explains failures, such as a model that isn't pulled, in "error".
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("ollama %s: status %d: %s", path, resp.StatusCode, e.Error)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("ollama %s: decode response: %w", path, err)
	}
	return nil
}
