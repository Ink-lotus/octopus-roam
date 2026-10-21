package update

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/Ink-lotus/octopus-roam/internal/utils/shutdown"
	"github.com/charmbracelet/log"
)

var shutdownUpdate = shutdown.Shutdown

func UpdateCore(targetVersion string) (*UpdateReceipt, error) {
	return updateCore(targetVersion)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), os.ModePerm); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func restartExecutable(execPath, backupDir string) {
	shutdownUpdate()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if testRuntimeGOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
			exitProcess(1)
			return
		}
		exitProcess(0)
		return
	}

	if err := execProcess(execPath, os.Args, os.Environ()); err == nil {
		return
	} else {
		log.Errorf("restarting failed: %v", err)
	}
	if err := restoreUpdateBackup(execPath, backupDir); err != nil {
		log.Errorf("restoring the previous executable failed: %v", err)
		exitProcess(1)
		return
	}
	if err := execProcess(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting the restored executable failed: %v", err)
		exitProcess(1)
	}
}

func restoreUpdateBackup(execPath, backupDir string) error {
	if backupDir == "" {
		return fmt.Errorf("update backup is unavailable")
	}
	backupExecutable := filepath.Join(backupDir, filepath.Base(execPath))
	if _, err := os.Stat(backupExecutable); err != nil {
		return fmt.Errorf("previous executable backup is unavailable: %w", err)
	}
	temporary := execPath + ".restore-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := copyFile(backupExecutable, temporary); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("copy previous executable: %w", err)
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("restore executable permissions: %w", err)
	}
	if err := os.Rename(temporary, execPath); err != nil {
		if runtime.GOOS != "windows" {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace failed executable: %w", err)
		}
		if removeErr := os.Remove(execPath); removeErr != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace failed executable: %w", err)
		}
		if renameErr := os.Rename(temporary, execPath); renameErr != nil {
			_ = os.Remove(temporary)
			return fmt.Errorf("replace failed executable: %w", renameErr)
		}
	}
	return nil
}
