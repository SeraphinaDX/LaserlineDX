package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"warp/internal/wire"
)

const maxFiles = 10000

// Shares contains an OS-enforced filesystem root. Directory symlinks are not
// traversed in listings; Root.Open prevents paths escaping after a race.
type Shares struct {
	path string
	only string
	root *os.Root
}

func NewShares(path string) (*Shares, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	dir := abs
	only := ""
	if !st.IsDir() {
		if !st.Mode().IsRegular() {
			return nil, errors.New("only regular files or directories can be shared")
		}
		dir, only = filepath.Dir(abs), filepath.Base(abs)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &Shares{path: dir, only: only, root: root}, nil
}

func (s *Shares) Close() error { return s.root.Close() }

func fileID(name string, st fs.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", name, st.Size(), st.ModTime().UnixNano())))
	return hex.EncodeToString(sum[:16])
}
func describe(name string, st fs.FileInfo) wire.File {
	return wire.File{ID: fileID(name, st), Name: filepath.ToSlash(name), Size: st.Size(), ModTime: st.ModTime().UnixNano(), IsDir: st.IsDir()}
}

func (s *Shares) List() ([]wire.File, error) {
	if s.only != "" {
		f, err := s.root.Open(s.only)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, errors.New("shared file is no longer regular")
		}
		return []wire.File{describe(s.only, st)}, nil
	}
	result := make([]wire.File, 0)
	err := filepath.WalkDir(s.path, func(path string, ent fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == s.path || (!ent.IsDir() && !ent.Type().IsRegular()) {
			return nil
		}
		if len(result) >= maxFiles {
			return fmt.Errorf("too many shared entries (maximum %d)", maxFiles)
		}
		rel, err := filepath.Rel(s.path, path)
		if err != nil {
			return err
		}
		// The shared root, not a join against an untrusted remote path, opens
		// the candidate to avoid following a swapped-out symlink outside root.
		f, err := s.root.Open(rel)
		if err != nil {
			return nil
		} // file changed while listing
		st, err := f.Stat()
		f.Close()
		if err == nil && (st.Mode().IsRegular() || st.IsDir()) {
			result = append(result, describe(rel, st))
		}
		return nil
	})
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name) })
	return result, err
}

func (s *Shares) OpenByID(id string) (*os.File, wire.File, error) {
	list, err := s.List()
	if err != nil {
		return nil, wire.File{}, err
	}
	for _, item := range list {
		if item.IsDir || item.ID != id {
			continue
		}
		f, err := s.root.Open(filepath.FromSlash(item.Name))
		if err != nil {
			return nil, wire.File{}, err
		}
		st, err := f.Stat()
		if err != nil || !st.Mode().IsRegular() || fileID(filepath.FromSlash(item.Name), st) != id {
			f.Close()
			return nil, wire.File{}, errors.New("file changed since listing; refresh and retry")
		}
		return f, item, nil
	}
	return nil, wire.File{}, errors.New("file is unavailable or has changed; refresh file list")
}
