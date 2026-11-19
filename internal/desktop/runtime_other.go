//go:build !windows

package desktop

import "os"

func renameJSONFile(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
