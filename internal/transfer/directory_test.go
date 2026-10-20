package transfer

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureDirectoryRejectsTraversalAndSymlinks(t *testing.T) {
	base := t.TempDir()
	for _, bad := range []string{"", ".", "../escape", "/tmp/escape", "bad/../escape", "bad\\escape"} {
		if err := EnsureDirectory(base, bad); err == nil {
			t.Fatalf("allowed unsafe path %q", bad)
		}
	}
	if err := EnsureDirectory(base, "nested/empty"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(base, "nested/empty")); err != nil || !info.IsDir() {
		t.Fatalf("folder not created: %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges may not be available")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(base, "redirect")); err != nil {
		t.Skip(err)
	}
	if err := EnsureDirectory(base, "redirect/stolen"); err == nil {
		t.Fatal("followed a symlink while creating downloaded folder")
	}
	if _, err := os.Stat(filepath.Join(outside, "stolen")); !os.IsNotExist(err) {
		t.Fatalf("created outside root: %v", err)
	}
}
