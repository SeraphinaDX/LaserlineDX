package transfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureDirectory creates the local counterpart of a tagged remote folder,
// including empty folders. Each component is opened within an os.Root so a
// malicious symlink cannot redirect writes outside the download location.
func EnsureDirectory(dest, name string) error {
	rel := filepath.FromSlash(name)
	if !filepath.IsLocal(rel) || rel == "." || name == "" || strings.ContainsRune(name, '\\') {
		return errors.New("unsafe remote directory name")
	}
	if dest == "" {
		dest = "."
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	cur := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid directory component")
		}
		cur = filepath.Join(cur, part)
		if err := root.Mkdir(cur, 0700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := root.Lstat(cur)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("unsafe destination directory: %s", cur)
		}
	}
	return nil
}
