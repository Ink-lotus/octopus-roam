//go:build windows

package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRenameJSONFileRetriesTransientReader(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	if err := WriteJSON(path, State{PID: 1, Created: 2}); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	result := make(chan error, 1)
	next := State{PID: 1, Created: 2, Ready: true}
	go func() {
		result <- WriteJSON(path, next)
	}()
	select {
	case err := <-result:
		t.Fatalf("replacement returned while reader still held: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("replacement did not recover after reader release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement retry did not finish")
	}
	data, err := os.ReadFile(path)
	var actual State
	if err != nil || json.Unmarshal(data, &actual) != nil || actual != next {
		t.Fatalf("replacement did not publish the complete new state: %v %s", err, data)
	}
	assertNoJSONTemporaryFiles(t, directory)
}

func TestWriteJSONPersistentDenialPreservesState(t *testing.T) {
	for _, kind := range []string{"held reader", "read-only destination"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "state.json")
			if err := WriteJSON(path, State{PID: 1, Created: 2}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "held reader" {
				reader, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
			} else {
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(path, 0600)
			}
			started := time.Now()
			err = WriteJSON(path, State{PID: 1, Created: 2, Ready: true})
			elapsed := time.Since(started)
			if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
				t.Fatalf("persistent denial was hidden: %v", err)
			}
			if elapsed < 1900*time.Millisecond || elapsed > 5*time.Second {
				t.Fatalf("retry duration out of bounds: %s", elapsed)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed replacement changed old state: %v", err)
			}
			assertNoJSONTemporaryFiles(t, directory)
		})
	}
}

func TestRenameJSONFileMissingSourceReturnsImmediately(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	before := []byte("existing record")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err := renameJSONFile(filepath.Join(directory, "missing"), path)
	if !os.IsNotExist(err) {
		t.Fatalf("missing source error changed: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("non-sharing failure was retried")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed rename changed destination: %v", err)
	}
}

func TestWriteJSONInvalidValuePreservesState(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	before := []byte("existing record")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if err := WriteJSON(path, make(chan int)); err == nil {
		t.Fatal("invalid JSON value accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("marshal failure changed destination: %v", err)
	}
	assertNoJSONTemporaryFiles(t, directory)
}

func assertNoJSONTemporaryFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatalf("unexpected files after WriteJSON: %v", entries)
	}
}
