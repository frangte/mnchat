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

	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/search/config"
	"github.com/frangte/mnchat/internal/search/server"
	"github.com/frangte/mnchat/internal/search/store"
)

// shutdownTimeout stays under the 10 s that `docker stop` waits before SIGKILL.
const shutdownTimeout = 8 * time.Second

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

	lis, err := net.Listen("tcp", cfg.GRPCListen)
	if err != nil {
		return fmt.Errorf("listen grpc: %w", err)
	}
	reg := metrics.NewRegistry()
	grpcSrv := server.NewGRPC(server.New(store.New(db)), server.NewMetrics(reg))
	adminSrv := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           server.AdminHandler(reg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Either listener failing stops the process, so a bad port can't leave it half up.
	failed := make(chan error, 2)
	go func() { failed <- grpcSrv.Serve(lis) }()
	go func() { failed <- adminSrv.ListenAndServe() }()
	slog.Info("search listening", "grpc", cfg.GRPCListen, "metrics", cfg.MetricsAddr)

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
