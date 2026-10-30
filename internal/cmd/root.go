// Package cmd wires the cobra CLI: each subcommand builds its own dependency graph.
package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

func Execute() error {
	root := &cobra.Command{
		Use:          "mnchat",
		Short:        "MNChat — multi-tenant team chat",
		SilenceUsage: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
		},
	}
	root.AddCommand(newAPICmd(), newMigrateCmd())
	return root.ExecuteContext(context.Background())
}
