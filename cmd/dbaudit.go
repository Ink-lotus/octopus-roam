package cmd

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db/integrity"
	"github.com/Ink-lotus/octopus-roam/internal/dbprofile"
	"github.com/spf13/cobra"
)

func newDatabaseAuditCommand() *cobra.Command {
	var sqlitePath, profile, alias string
	command := &cobra.Command{Use: "db-audit", Short: "Read-only relationship and primary-key audit; never migrate or repair", SilenceErrors: true, SilenceUsage: true}
	command.Flags().StringVar(&sqlitePath, "sqlite-path", "", "existing SQLite file to open in read-only mode")
	command.Flags().StringVar(&profile, "profile", "", "server database profile file (read-only audit)")
	command.Flags().StringVar(&alias, "alias", "", "exact server profile alias")
	command.RunE = func(cmd *cobra.Command, args []string) error {
		dialect, dsn := "sqlite", sqlitePath
		if sqlitePath == "" {
			p, err := dbprofile.LoadFile(profile, alias)
			if err != nil {
				return integrity.ErrAuditUnavailable
			}
			spec, err := p.BuildConnectionSpec()
			if err != nil {
				return integrity.ErrAuditUnavailable
			}
			dialect, dsn = spec.Type, spec.DSN
		} else if profile != "" || alias != "" {
			return integrity.ErrAuditUnavailable
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		report, err := integrity.Audit(ctx, dialect, dsn)
		if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(report); encodeErr != nil {
			return integrity.ErrAuditUnavailable
		}
		if err != nil {
			return err
		}
		return report.Error()
	}
	return command
}

func init() { rootCmd.AddCommand(newDatabaseAuditCommand()) }
