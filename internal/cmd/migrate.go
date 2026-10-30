package cmd

import (
	"github.com/spf13/cobra"

	"github.com/frangte/mnchat/internal/config"
	"github.com/frangte/mnchat/internal/infra/mysqldb"
)

func newMigrateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations and exit",
		RunE: func(cmd *cobra.Command, _ []string) error {
			db, err := mysqldb.Open(cmd.Context(), config.Load().MySQLDSN)
			if err != nil {
				return err
			}
			defer db.Close()
			return mysqldb.Migrate(cmd.Context(), db)
		},
	}
}
