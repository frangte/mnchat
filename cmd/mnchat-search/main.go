// Command mnchat-search serves the SearchService gRPC API from the MySQL replica.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/search/ai"
	"github.com/frangte/mnchat/internal/search/config"
	"github.com/frangte/mnchat/internal/search/server"
	"github.com/frangte/mnchat/internal/search/store"
)

const (
	// shutdownTimeout stays under the 10 s that `docker stop` waits before SIGKILL.
	shutdownTimeout = 8 * time.Second

	warmUpTimeout = 30 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("search stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	db, err := store.Open(ctx, cfg.MySQLDSN)
	if err != nil {
		return err
	}
	defer db.Close()

	reg := metrics.NewRegistry()
	models, ollama, err := newAI(cfg, reg)
	if err != nil {
		return err
	}

	lis, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}
	grpcSrv := server.NewGRPC(server.New(store.New(db), models), server.NewMetrics(reg))
	adminSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           server.AdminHandler(reg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Either listener failing stops the process, so a bad port can't leave it half up.
	failed := make(chan error, 2)
	go func() { failed <- grpcSrv.Serve(lis) }()
	go func() { failed <- adminSrv.ListenAndServe() }()
	if ollama != nil {
		go warmEmbedModel(ctx, ollama)
	}
	slog.Info("search listening", "grpc", cfg.GRPCListen, "metrics", cfg.MetricsAddr, "ai", cfg.AIProvider)

	select {
	case <-ctx.Done():
		slog.Info("search shutting down")
	case err = <-failed:
	}

	// gRPC first, so /metrics can still report the requests that are draining.
	grpcSrv.Shutdown(shutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return errors.Join(err, adminSrv.Shutdown(shutdownCtx))
}

// newAI picks the model provider from AI_PROVIDER. It returns the Ollama client
// too, for the warm-up, or nil with the fake. Only a bad setting fails: Ollama
// being down is reported per request, so text search works without it.
func newAI(cfg config.Config, reg prometheus.Registerer) (server.AI, *ai.Ollama, error) {
	aiMetrics := ai.NewMetrics(reg)
	switch cfg.AIProvider {
	case "ollama":
		o, err := ai.NewOllama(ai.OllamaConfig{
			URL:        cfg.OllamaURL,
			ChatModel:  cfg.OllamaChatModel,
			EmbedModel: cfg.OllamaEmbedModel,
		}, aiMetrics)
		if err != nil {
			return server.AI{}, nil, err
		}
		return server.AI{Summarizer: o, Embedder: o, Model: cfg.OllamaChatModel}, o, nil
	case "fake":
		return server.AI{Summarizer: ai.Fake{}, Embedder: ai.Fake{}, Model: ai.FakeModel}, nil, nil
	default:
		return server.AI{}, nil, fmt.Errorf("AI_PROVIDER %q: want ollama or fake", cfg.AIProvider)
	}
}

// warmEmbedModel loads the embedding model in the background. A cold load took
// 1.1-2.2 s, about chat's 2 s SearchThreads deadline, so the first semantic search
// after a restart would fail. Serving never waits for this, and a failure is only
// logged: Ollama may simply not be running yet.
func warmEmbedModel(ctx context.Context, o *ai.Ollama) {
	ctx, cancel := context.WithTimeout(ctx, warmUpTimeout)
	defer cancel()
	start := time.Now()
	if err := o.WarmUp(ctx); err != nil {
		slog.Warn("embedding model warm-up failed, the first semantic search may be slow", "err", err)
		return
	}
	slog.Info("embedding model warm", "took", time.Since(start).Round(time.Millisecond))
}
