//go:build !windows

package update

import "os/exec"

func scheduleWindowsUpdate(string, string, string) (*exec.Cmd, error) {
	return nil, ErrUnsupportedPlatform
}
