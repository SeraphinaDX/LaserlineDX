// Package transfer implements control connections and restartable downloads.
package transfer

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"warp/internal/hub"
	"warp/internal/invite"
	"warp/internal/wire"
)

type Client struct {
	conn       net.Conn
	mu         sync.Mutex
	Updates    chan wire.Message
	Done       chan error
	Invitation invite.Code
}

func Join(ctx context.Context, code invite.Code, nick string) (*Client, wire.Message, error) {
	conn, err := hub.Dial(ctx, code)
	if err != nil {
		return nil, wire.Message{}, err
	}
	conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := wire.Write(conn, wire.Message{Type: "hello", Role: "control", Name: nick, Token: hex.EncodeToString(code.Secret[:])}); err != nil {
		conn.Close()
		return nil, wire.Message{}, err
	}
	welcome, err := wire.Read(conn)
	if err != nil {
		conn.Close()
		return nil, wire.Message{}, err
	}
	if welcome.Type == "error" {
		conn.Close()
		return nil, welcome, errors.New(welcome.Error)
	}
	if welcome.Type != "welcome" {
		conn.Close()
		return nil, welcome, fmt.Errorf("unexpected response: %s", welcome.Type)
	}
	conn.SetDeadline(time.Time{})
	c := &Client{conn: conn, Invitation: code, Updates: make(chan wire.Message, 128), Done: make(chan error, 1)}
	go func() {
		defer close(c.Updates)
		for {
			m, err := wire.Read(conn)
			if err != nil {
				c.Done <- err
				return
			}
			// Backpressure on the UI should never cause unbounded allocation.
			select {
			case c.Updates <- m:
			default:
			}
		}
	}()
	return c, welcome, nil
}
func (c *Client) Send(m wire.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := wire.Write(c.conn, m)
	c.conn.SetWriteDeadline(time.Time{})
	return err
}
func (c *Client) Close() error { return c.conn.Close() }

type Progress struct {
	Name    string
	Bytes   int64
	Total   int64
	Attempt int
	Message string
}

type manifest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Download retries interrupted TLS sessions, reopening the part file at its
// verified offset. Interrupted downloads remain on disk between program runs.
func Download(ctx context.Context, code invite.Code, entry wire.File, dest string, notify func(Progress)) error {
	if entry.IsDir || !filepath.IsLocal(filepath.FromSlash(entry.Name)) || entry.Size < 0 || entry.ID == "" {
		return errors.New("unsafe or invalid remote filename")
	}
	if dest == "" {
		dest = "."
	}
	if err := os.MkdirAll(dest, 0700); err != nil {
		return err
	}
	out := filepath.Join(dest, filepath.FromSlash(entry.Name))
	if err := makeSafeDirs(dest, filepath.FromSlash(entry.Name)); err != nil {
		return err
	}
	// Refuse to overwrite an existing complete download, even when a remote
	// peer reuses the same filename for different content.
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("destination already exists: %s", out)
	} else if !os.IsNotExist(err) {
		return err
	}
	part := out + ".warp-part"
	sidecar := part + ".json"
	if err := preparePart(part, sidecar, entry); err != nil {
		return err
	}
	var last error
	for attempt := 1; attempt <= 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		last = downloadOnce(ctx, code, entry, part, sidecar, out, attempt, notify)
		if last == nil {
			return nil
		}
		if errors.Is(last, errMismatch) {
			return last
		}
		notify(Progress{Name: entry.Name, Total: entry.Size, Attempt: attempt, Message: "Interrupted: " + last.Error()})
		if attempt < 4 {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("transfer paused; rerun Warp and select the file to resume: %w", last)
}

// Use os.Root when creating directory trees to keep a compromised directory
// entry from redirecting downloads outside the selected downloads root.
func makeSafeDirs(dest, relative string) error {
	parent := filepath.Dir(relative)
	if parent == "." {
		return nil
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	cur := ""
	for _, part := range strings.Split(parent, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid directory component")
		}
		cur = filepath.Join(cur, part)
		err := root.Mkdir(cur, 0700)
		if err != nil && !os.IsExist(err) {
			return err
		}
		info, err := root.Lstat(cur)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("unsafe destination directory: %s", cur)
		}
	}
	return nil
}
func preparePart(part, sidecar string, entry wire.File) error {
	st, err := os.Lstat(part)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var m manifest
	// A pre-existing symlink must not redirect manifest writes.
	if info, checkErr := os.Lstat(sidecar); checkErr == nil {
		if !info.Mode().IsRegular() {
			return errors.New("partial download manifest is not a regular file")
		}
	} else if !os.IsNotExist(checkErr) {
		return checkErr
	}
	b, e := os.ReadFile(sidecar)
	if e == nil {
		_ = json.Unmarshal(b, &m)
	}
	valid := err == nil && st.Mode().IsRegular() && m.ID == entry.ID && m.Name == entry.Name && m.Size == entry.Size && st.Size() <= entry.Size
	if err == nil && !st.Mode().IsRegular() {
		return errors.New("partial download is not a regular file")
	}
	if !valid {
		// Do not silently destroy an earlier partial download for a different
		// version of a remote file.
		if err == nil && (m.ID != "" && m.ID != entry.ID) {
			return fmt.Errorf("a partial file for another version exists at %s; move it aside to restart", part)
		}
		f, e := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			return e
		}
	}
	info, _ := json.Marshal(manifest{entry.ID, entry.Name, entry.Size})
	return os.WriteFile(sidecar, info, 0600)
}

var errMismatch = errors.New("file checksum failed; partial file retained for inspection (remove .warp-part to restart)")

func downloadOnce(ctx context.Context, code invite.Code, entry wire.File, part, sidecar, out string, attempt int, notify func(Progress)) error {
	f, err := os.OpenFile(part, os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	offset := st.Size()
	if offset > entry.Size {
		return errors.New("partial file is larger than remote file")
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	conn, err := hub.Dial(ctx, code)
	if err != nil {
		return err
	}
	defer conn.Close()
	// Context cancellation must also interrupt an active stream read.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()
	defer close(done)
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := wire.Write(conn, wire.Message{Type: "hello", Role: "download", Token: hex.EncodeToString(code.Secret[:])}); err != nil {
		return err
	}
	if err := wire.Write(conn, wire.Message{Type: "get", File: entry, Offset: offset}); err != nil {
		return err
	}
	header, err := wire.Read(conn)
	if err != nil {
		return err
	}
	if header.Type == "error" {
		return errors.New(header.Error)
	}
	if header.Type != "start" || header.File.ID != entry.ID || header.File.Size != entry.Size || header.Offset != offset || len(header.SHA256) != 64 {
		return errors.New("unexpected transfer metadata")
	}
	expected, err := hex.DecodeString(header.SHA256)
	if err != nil {
		return err
	}
	notify(Progress{Name: entry.Name, Bytes: offset, Total: entry.Size, Attempt: attempt, Message: "Receiving encrypted file"})
	buf := make([]byte, 128*1024)
	remaining := entry.Size - offset
	lastUpdate := time.Now().Add(-time.Second)
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := int64(len(buf))
		if remaining < size {
			size = remaining
		}
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		n, err := io.ReadFull(conn, buf[:int(size)])
		if n > 0 {
			if writeErr := writeFull(f, buf[:n]); writeErr != nil {
				return writeErr
			}
			remaining -= int64(n)
			offset += int64(n)
			if time.Since(lastUpdate) > 150*time.Millisecond || remaining == 0 {
				notify(Progress{Name: entry.Name, Bytes: offset, Total: entry.Size, Attempt: attempt, Message: "Receiving encrypted file"})
				lastUpdate = time.Now()
			}
		}
		if err != nil {
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	check, err := os.Open(part)
	if err != nil {
		return err
	}
	hash := sha256.New()
	_, err = io.Copy(hash, check)
	check.Close()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(hash.Sum(nil), expected) != 1 {
		return errMismatch
	}
	if err := f.Close(); err != nil {
		return err
	}
	if _, err := os.Lstat(out); err == nil {
		return errors.New("destination was created by another program; refusing overwrite")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(part, out); err != nil {
		return err
	}
	_ = os.Remove(sidecar)
	notify(Progress{Name: entry.Name, Bytes: entry.Size, Total: entry.Size, Attempt: attempt, Message: "Download complete and SHA-256 verified"})
	return nil
}
func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if n > 0 {
			b = b[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
