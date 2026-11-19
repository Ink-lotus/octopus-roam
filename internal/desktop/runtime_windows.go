//go:build windows

package desktop

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameJSONFile keeps the complete-file publication contract while allowing
// a short-lived Windows reader to release its handle.
func renameJSONFile(oldPath, newPath string) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := os.Rename(oldPath, newPath)
		if err == nil || (!errors.Is(err, windows.ERROR_ACCESS_DENIED) &&
			!errors.Is(err, windows.ERROR_SHARING_VIOLATION) &&
			!errors.Is(err, windows.ERROR_LOCK_VIOLATION)) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
}
