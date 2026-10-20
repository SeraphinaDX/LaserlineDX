package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryListingAndNoSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0700)
	os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("hello"), 0600)
	os.WriteFile(filepath.Join(dir, "z.txt"), []byte("world"), 0600)
	if err := os.Symlink("sub/a.txt", filepath.Join(dir, "alias")); err != nil {
		t.Skip(err)
	}
	shares, err := NewShares(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer shares.Close()
	files, err := shares.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d: %+v", len(files), files)
	}
	if _, _, err := shares.OpenByID(files[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := shares.OpenByID("../../something"); err == nil {
		t.Fatal("accepted arbitrary file identifier")
	}
}
