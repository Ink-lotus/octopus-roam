package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/db"
	"github.com/Ink-lotus/octopus-roam/internal/dbprofile"
	"github.com/Ink-lotus/octopus-roam/internal/model"
	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

var (
	dbProfileFile          string
	dbProfileAlias         string
	dbProfileRun           bool
	errDatabaseCheckFailed = errors.New("database check reported a failure")
)

type databaseCheckResult struct {
	Status                    string   `json:"status"`
	Alias                     string   `json:"alias"`
	Type                      string   `json:"type,omitempty"`
	RestoreDatabaseConfigured bool     `json:"restore_database_configured,omitempty"`
	Checks                    []string `json:"checks,omitempty"`
	Error                     string   `json:"error,omitempty"`
	DurationMS                int64    `json:"duration_ms,omitempty"`
}

var databaseCheckCmd = &cobra.Command{
	Use:           "db-check",
	Short:         "Validate or run one isolated database acceptance profile",
	SilenceErrors: true,
	SilenceUsage:  true,
	RunE: func(cmd *cobra.Command, args []string) error {
		started := time.Now()
		result := databaseCheckResult{Status: "invalid_profile", Alias: dbProfileAlias}
		profile, err := dbprofile.LoadFile(profilePath(), dbProfileAlias)
		if err != nil {
			result.Error = classifyProfileError(err)
			writeDatabaseCheckResult(result, started)
			return errDatabaseCheckFailed
		}
		result.Status = "profile_valid"
		result.Type = profile.Type
		result.RestoreDatabaseConfigured = profile.RestoreDatabase != ""
		result.Checks = []string{"profile_structure", "alias_selection", "tls_policy"}
		if !dbProfileRun {
			writeDatabaseCheckResult(result, started)
			return nil
		}
		result.Status = "test_failed"
		spec, err := profile.BuildConnectionSpec()
		if err != nil {
			result.Error = classifyProfileError(err)
			writeDatabaseCheckResult(result, started)
			return errDatabaseCheckFailed
		}
		if err := runDatabaseSmoke(context.Background(), spec.Type, spec.DSN); err != nil {
			result.Error = classifyDatabaseError(err)
			writeDatabaseCheckResult(result, started)
			return errDatabaseCheckFailed
		}
		result.Status = "passed"
		result.Checks = append(result.Checks, "schema_migration", "cache_read", "database_read_write")
		writeDatabaseCheckResult(result, started)
		return nil
	},
}

func init() {
	databaseCheckCmd.Flags().StringVar(&dbProfileFile, "profile", "", "profile file outside the repository")
	databaseCheckCmd.Flags().StringVar(&dbProfileAlias, "alias", "", "exact profile alias")
	databaseCheckCmd.Flags().BoolVar(&dbProfileRun, "connect", false, "connect and run the database smoke suite")
	rootCmd.AddCommand(databaseCheckCmd)
}

func profilePath() string {
	if dbProfileFile != "" {
		return dbProfileFile
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "octopus-roam", "db-profiles.json")
	}
	return filepath.Join(".", "db-profiles.json")
}

func writeDatabaseCheckResult(result databaseCheckResult, started time.Time) {
	result.DurationMS = time.Since(started).Milliseconds()
	data, err := json.Marshal(result)
	if err == nil {
		fmt.Println(string(data))
	}
}

func classifyProfileError(err error) string {
	switch {
	case errors.Is(err, dbprofile.ErrProfileMissing):
		return "profile_missing"
	case errors.Is(err, dbprofile.ErrAliasMissing):
		return "alias_missing"
	default:
		return "profile_invalid"
	}
}

func classifyDatabaseError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "x509"), strings.Contains(message, "tls"), strings.Contains(message, "ssl"), strings.Contains(message, "certificate"):
		return "tls_validation_failed"
	case strings.Contains(message, "password"), strings.Contains(message, "authentication"), strings.Contains(message, "access denied"), strings.Contains(message, "28p01"):
		return "authentication_failed"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"), strings.Contains(message, "i/o timeout"):
		return "connection_timeout"
	default:
		return "test_failed"
	}
}

func runDatabaseSmoke(ctx context.Context, dbType, dsn string) error {
	if err := db.InitDB(dbType, dsn, false); err != nil {
		return err
	}
	defer db.Close()
	if err := db.GetDB().WithContext(ctx).Exec("SELECT 1").Error; err != nil {
		return err
	}
	if err := op.InitCache(); err != nil {
		return err
	}
	database := db.GetDB().WithContext(ctx)
	var setting model.Setting
	if err := database.Where(&model.Setting{Key: model.SettingKeyStatsSaveInterval}).Take(&setting).Error; err != nil {
		return err
	}
	// This is a no-op update against an application setting. It proves the
	// account can write the migrated schema without leaving synthetic data.
	if tx := database.Model(&model.Setting{}).
		Where(&model.Setting{Key: model.SettingKeyStatsSaveInterval}).
		Update("value", setting.Value); tx.Error != nil {
		return tx.Error
	} else if tx.RowsAffected > 1 {
		return fmt.Errorf("database setting write affected %d rows", tx.RowsAffected)
	}
	return nil
}

func buildSettingTouchSQL(dialect gorm.Dialector, value string) (string, error) {
	database, err := gorm.Open(dialect, &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		return "", err
	}
	if sqlDB, dbErr := database.DB(); dbErr == nil {
		defer sqlDB.Close()
	}
	tx := database.Model(&model.Setting{}).
		Where(&model.Setting{Key: model.SettingKeyStatsSaveInterval}).
		Update("value", value)
	if tx.Error != nil {
		return "", tx.Error
	}
	return tx.Statement.SQL.String(), nil
}
