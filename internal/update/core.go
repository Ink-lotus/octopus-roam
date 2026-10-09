package update

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/Ink-lotus/octopus-roam/internal/utils/shutdown"
	"github.com/charmbracelet/log"
)

func UpdateCore() error {
	// 必须在任何网络、文件替换与重启行为之前拒绝，包括旧客户端的调用。
	return ErrSelfUpdateDisabled
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

// getDownloadFilename 返回当前平台对应的发布归档名称。
func getDownloadFilename() (string, error) {
	arch := runtime.GOARCH
	goos := runtime.GOOS

	switch goos {
	case "windows":
		switch arch {
		case "amd64":
			return "octopus-roam-windows-amd64.zip", nil
		}
	case "darwin":
		switch arch {
		case "amd64":
			return "octopus-roam-darwin-amd64.zip", nil
		case "arm64":
			return "octopus-roam-darwin-arm64.zip", nil
		}
	case "linux":
		switch arch {
		case "386":
			return "octopus-roam-linux-386.zip", nil
		case "amd64":
			return "octopus-roam-linux-amd64.zip", nil
		case "arm":
			return "octopus-roam-linux-arm.zip", nil
		case "arm64":
			return "octopus-roam-linux-arm64.zip", nil
		}
	case "android":
		switch arch {
		case "386":
			return "octopus-roam-android-386.zip", nil
		case "amd64":
			return "octopus-roam-android-amd64.zip", nil
		case "arm":
			return "octopus-roam-android-arm.zip", nil
		case "arm64":
			return "octopus-roam-android-arm64.zip", nil
		}
	}
	return "", fmt.Errorf("unsupported platform: %s/%s", goos, arch)
}

func restartExecutable(execPath string) {
	shutdown.Shutdown()

	log.Infof("restarting: %q %q", execPath, os.Args[1:])

	if runtime.GOOS == "windows" {
		cmd := exec.Command(execPath, os.Args[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			log.Errorf("restarting failed: %v", err)
		}
		os.Exit(0)
	}

	if err := syscall.Exec(execPath, os.Args, os.Environ()); err != nil {
		log.Errorf("restarting failed: %v", err)
	}
}
