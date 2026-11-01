package cmd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/frangte/mnchat/internal/config"
	"github.com/frangte/mnchat/internal/httpapi"
	"github.com/frangte/mnchat/internal/infra/mysqldb"
	"github.com/frangte/mnchat/internal/platform/metrics"
	"github.com/frangte/mnchat/internal/realtime"
	"github.com/frangte/mnchat/internal/repo"
	"github.com/frangte/mnchat/internal/searchclient"
	"github.com/frangte/mnchat/internal/service"
)

func newAPICmd() *cobra.Command {
	var migrate bool
	c := &cobra.Command{
		Use:   "api",
		Short: "Run the HTTP + WebSocket API server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			cfg := config.Load()
			db, err := mysqldb.Open(ctx, cfg.MySQLDSN)
			if err != nil {
				return err
			}
			defer db.Close()
			if migrate {
				if err := mysqldb.Migrate(ctx, db); err != nil {
					return err
				}
			}

			reg := metrics.NewRegistry()
			apiMetrics := metrics.NewAPI(reg)

			hub := realtime.NewHub(apiMetrics)
			authSvc := service.NewAuthService(repo.NewUserRepo(db), cfg.JWTSecret)
			workspaces, channels, messages := repo.NewWorkspaceRepo(db), repo.NewChannelRepo(db), repo.NewMessageRepo(db)
			chatSvc := service.NewChatService(workspaces, channels, messages, hub, apiMetrics)

			searchClient, err := searchclient.New(cfg.SearchGRPCAddr, apiMetrics)
			if err != nil {
				return err
			}
			defer searchClient.Close()
			searchSvc := service.NewSearchService(workspaces, channels, messages, searchClient)

			srv := &http.Server{
				Addr:              cfg.HTTPAddr,
				Handler:           httpapi.NewRouter(authSvc, chatSvc, searchSvc, hub, db, apiMetrics, metrics.Handler(reg), cfg.WSOrigins),
				ReadHeaderTimeout: 5 * time.Second,
			}
			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				hub.Close() // hijacked WebSocket conns are not closed by Shutdown
				_ = srv.Shutdown(shutdownCtx)
			}()

			slog.Info("api listening", "addr", cfg.HTTPAddr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	}
	c.Flags().BoolVar(&migrate, "migrate", true, "apply database migrations on startup")
	return c
}
