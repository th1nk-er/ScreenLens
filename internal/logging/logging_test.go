package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWritesToConfiguredFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "screenlens.log")
	handle, err := Open(path, false)
	if err != nil {
		t.Fatal(err)
	}
	handle.Logger.Info("test log entry")
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test log entry") {
		t.Fatalf("log file does not contain test entry: %q", string(data))
	}
}

func TestResolvePathUsesExecutableDirectoryByDefault(t *testing.T) {
	path, err := ResolvePath("")
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(filepath.Dir(executable), defaultFileName)
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}
