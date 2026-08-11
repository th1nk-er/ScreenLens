package artifact

import (
	"os"
	"runtime"
	"testing"
)

func TestStageInDirCreatesPrivateArtifactAndCleanupIsIdempotent(t *testing.T) {
	image, err := Stage([]byte("image"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if image.Path == "" || image.Dir == "" {
		t.Fatal("expected artifact paths")
	}
	if runtime.GOOS != "windows" {
		if mode := fileMode(t, image.Path); mode.Perm() != 0o600 {
			t.Fatalf("artifact mode = %o, want 600", mode.Perm())
		}
	}
	if err := image.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if err := image.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(image.Dir); !os.IsNotExist(err) {
		t.Fatalf("artifact directory still exists: %v", err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
