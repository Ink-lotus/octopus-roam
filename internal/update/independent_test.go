package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentBuildDisablesSelfUpdate(t *testing.T) {
	// 使用空临时目录确认两个入口不会创建更新文件或返回可安装的上游版本。
	dir := t.TempDir()
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	t.Setenv("TMPDIR", dir)
	if info, err := GetLatestInfo(); info != nil || !errors.Is(err, ErrSelfUpdateDisabled) {
		t.Fatalf("latest info=%v err=%v", info, err)
	}
	if err := UpdateCore(); !errors.Is(err, ErrSelfUpdateDisabled) {
		t.Fatalf("update=%v", err)
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled updater wrote files")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable + ".old"); !os.IsNotExist(err) {
		t.Fatal("disabled updater renamed executable")
	}
}
