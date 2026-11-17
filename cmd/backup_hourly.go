package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Ink-lotus/octopus-roam/internal/op"
	"github.com/spf13/cobra"
)

func newBackupHourlyCommand() *cobra.Command {
	var input, output, passwordEnv string
	var acknowledged bool
	c := &cobra.Command{Use: "backup-convert-hourly", Short: "Archive legacy invalid hours in a NEW encrypted backup; never open a database", SilenceErrors: true, SilenceUsage: true}
	c.Flags().StringVar(&input, "input", "", "existing encrypted backup")
	c.Flags().StringVar(&output, "output", "", "new encrypted output path; must not exist")
	c.Flags().StringVar(&passwordEnv, "password-env", "", "environment variable holding the backup password")
	c.Flags().BoolVar(&acknowledged, "archive-legacy-hours", false, "explicitly quarantine hours >=24; no inferred correction or summing")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if input == "" || output == "" || passwordEnv == "" || !acknowledged {
			return fmt.Errorf("input, output, password-env and archive-legacy-hours are required")
		}
		password := os.Getenv(passwordEnv)
		if strings.TrimSpace(password) == "" {
			return fmt.Errorf("backup password environment variable is empty")
		}
		file, err := os.Open(input)
		if err != nil {
			return fmt.Errorf("cannot read input backup")
		}
		raw, readErr := io.ReadAll(io.LimitReader(file, op.MaxBackupRequestBytes+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil {
			return fmt.Errorf("cannot read input backup")
		}
		if len(raw) > op.MaxBackupRequestBytes {
			return op.ErrBackupTooLarge
		}
		dump, err := op.DecryptBackup(raw, password)
		if err != nil {
			return err
		}
		converted, count, err := op.ConvertLegacyHourlyBackup(dump)
		if err != nil {
			return err
		}
		encrypted, err := op.EncryptBackup(converted, password)
		if err != nil {
			return err
		}
		// Refuse overwrite, symlinks to existing files and input=output.
		out, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("output must be a writable new file")
		}
		n, writeErr := out.Write(encrypted)
		if writeErr == nil && n != len(encrypted) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			writeErr = out.Sync()
		}
		closeErr = out.Close()
		if writeErr != nil || closeErr != nil {
			return fmt.Errorf("output write failed; input is unchanged; preserve and inspect the incomplete output")
		}
		hash := func(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"version": 1004, "archived_rows": count, "input_sha256": hash(raw), "output_sha256": hash(encrypted), "database_opened": false})
	}
	return c
}

func init() { rootCmd.AddCommand(newBackupHourlyCommand()) }
