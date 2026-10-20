package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryListingAndNoSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0700)
	os.Mkdir(filepath.Join(dir, "empty"), 0700)
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
	if len(files) != 4 {
		t.Fatalf("want 2 files and 2 directories, got %d: %+v", len(files), files)
	}
	dirs, regular := 0, 0
	for _, file := range files {
		if file.IsDir {
			dirs++
			if _, _, err := shares.OpenByID(file.ID); err == nil {
				t.Fatal("directory identifier was accepted as download")
			}
		} else {
			regular++
			f, _, err := shares.OpenByID(file.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
	}
	if dirs != 2 || regular != 2 {
		t.Fatalf("wrong directory/file split: %+v", files)
	}
	if _, _, err := shares.OpenByID("../../something"); err == nil {
		t.Fatal("accepted arbitrary file identifier")
	}
}
