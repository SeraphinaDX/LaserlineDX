// Package wire implements bounded length-prefixed JSON frames. A binary file
// stream may follow a frame without decoder read-ahead consuming file bytes.
package wire

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const MaxFrame = 4 << 20

type File struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
}

type Message struct {
	Type   string `json:"type"`
	Token  string `json:"token,omitempty"`
	Role   string `json:"role,omitempty"`
	Name   string `json:"name,omitempty"`
	Text   string `json:"text,omitempty"`
	Files  []File `json:"files,omitempty"`
	File   File   `json:"file"`
	Offset int64  `json:"offset,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Users  int    `json:"users,omitempty"`
	Error  string `json:"error,omitempty"`
}

func Write(w io.Writer, m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(b) > MaxFrame {
		return errors.New("frame exceeds maximum size")
	}
	header := [4]byte{}
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, b)
}

func Read(r io.Reader) (Message, error) {
	var m Message
	header := [4]byte{}
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return m, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxFrame {
		return m, fmt.Errorf("invalid frame size: %d", size)
	}
	b := make([]byte, int(size))
	if _, err := io.ReadFull(r, b); err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, err
	}
	return m, nil
}

func writeAll(w io.Writer, b []byte) error {
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
