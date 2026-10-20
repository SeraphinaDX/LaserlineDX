// Package hub serves a shared file area and an ephemeral encrypted chat room.
package hub

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"warp/internal/invite"
	"warp/internal/wire"
)

const maxPeers = 64
const maxConcurrentDownloads = 32

type Options struct {
	Path      string
	Listen    string
	Advertise string
}

type Server struct {
	listener  net.Listener
	shares    *Shares
	code      invite.Code
	mu        sync.Mutex
	peers     map[*peer]bool
	transfers chan struct{}
	Events    chan string
	closed    bool
}

type peer struct {
	conn net.Conn
	mu   sync.Mutex
	name string
}

func (p *peer) send(m wire.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err := wire.Write(p.conn, m)
	p.conn.SetWriteDeadline(time.Time{})
	return err
}

func Start(o Options) (*Server, error) {
	if o.Path == "" {
		o.Path = "."
	}
	if o.Listen == "" {
		o.Listen = ":47477"
	}
	shares, err := NewShares(o.Path)
	if err != nil {
		return nil, err
	}
	cert, fingerprint, err := newIdentity()
	if err != nil {
		shares.Close()
		return nil, err
	}
	listener, err := net.Listen("tcp", o.Listen)
	if err != nil {
		shares.Close()
		return nil, err
	}
	host := o.Advertise
	if host == "" {
		host = guessLocalIP()
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		// Accept an IPv6 literal as the advertise hostname.
		host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	address := net.JoinHostPort(host, port)
	if len(address) > 255 {
		listener.Close()
		shares.Close()
		return nil, errors.New("advertised address is too long")
	}
	s := &Server{
		listener: tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}),
		shares:   shares,
		code:     invite.Code{Address: address, Fingerprint: fingerprint},
		peers:    make(map[*peer]bool), transfers: make(chan struct{}, maxConcurrentDownloads),
		Events: make(chan string, 256),
	}
	if _, err := rand.Read(s.code.Secret[:]); err != nil {
		s.listener.Close()
		shares.Close()
		return nil, err
	}
	go s.run()
	s.log("Warp server listening at " + listener.Addr().String())
	return s, nil
}

func (s *Server) Invitation() string { return s.code.String() }
func (s *Server) Address() string    { return s.listener.Addr().String() }
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	peers := make([]*peer, 0, len(s.peers))
	for p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	err := s.listener.Close()
	for _, p := range peers {
		p.conn.Close()
	}
	// Existing downloads hold a file descriptor and finish or stop on their
	// own connection. The root is no longer used for new requests.
	return errors.Join(err, s.shares.Close())
}
func (s *Server) log(line string) {
	select {
	case s.Events <- line:
	default:
	}
}
func (s *Server) run() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.serveConn(c)
	}
}
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(12 * time.Second))
	first, err := wire.Read(conn)
	if err != nil || first.Type != "hello" {
		return
	}
	token, err := hex.DecodeString(first.Token)
	if err != nil || len(token) != len(s.code.Secret) || subtle.ConstantTimeCompare(token, s.code.Secret[:]) != 1 {
		_ = wire.Write(conn, wire.Message{Type: "error", Error: "invalid invitation secret"})
		return
	}
	conn.SetDeadline(time.Time{})
	switch first.Role {
	case "download":
		select {
		case s.transfers <- struct{}{}:
			defer func() { <-s.transfers }()
			s.download(conn)
		default:
			wire.Write(conn, wire.Message{Type: "error", Error: "download capacity reached; retry shortly"})
		}
	case "control":
		s.control(conn, first.Name)
	default:
		wire.Write(conn, wire.Message{Type: "error", Error: "unknown connection role"})
	}
}
func (s *Server) control(conn net.Conn, requested string) {
	p := &peer{conn: conn}
	files, err := s.shares.List()
	if err != nil {
		p.send(wire.Message{Type: "error", Error: err.Error()})
		return
	}
	s.mu.Lock()
	if s.closed || len(s.peers) >= maxPeers {
		s.mu.Unlock()
		p.send(wire.Message{Type: "error", Error: "room full or closing"})
		return
	}
	name := sanitize(requested, 32)
	if name == "" {
		name = "Guest"
	}
	taken := make(map[string]bool, len(s.peers))
	for other := range s.peers {
		taken[other.name] = true
	}
	base := name
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	p.name = name
	s.peers[p] = true
	count := len(s.peers)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.peers, p)
		count := len(s.peers)
		s.mu.Unlock()
		s.broadcast(wire.Message{Type: "notice", Text: p.name + " left the room", Users: count})
		s.log(p.name + " disconnected")
	}()
	if err := p.send(wire.Message{Type: "welcome", Name: name, Files: files, Users: count, Text: "Warp BBS: downloads and encrypted chat"}); err != nil {
		return
	}
	s.log(p.name + " joined")
	s.broadcast(wire.Message{Type: "notice", Text: p.name + " joined the room", Users: count})
	for {
		m, err := wire.Read(conn)
		if err != nil {
			return
		}
		switch m.Type {
		case "list":
			files, err := s.shares.List()
			if err != nil {
				p.send(wire.Message{Type: "error", Error: err.Error()})
				continue
			}
			if err := p.send(wire.Message{Type: "files", Files: files}); err != nil {
				return
			}
		case "chat":
			if len(m.Text) > 2000 || !utf8.ValidString(m.Text) {
				continue
			}
			body := sanitize(m.Text, 1000)
			if body != "" {
				s.broadcast(wire.Message{Type: "chat", Name: name, Text: body})
			}
		case "ping":
			if err := p.send(wire.Message{Type: "pong"}); err != nil {
				return
			}
		default:
			p.send(wire.Message{Type: "error", Error: "unknown command"})
		}
	}
}
func sanitize(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if b.Len() >= limit {
			break
		}
		if r == '\n' || r == '\r' || r == '\x1b' || unicode.IsControl(r) {
			continue
		}
		if b.Len()+utf8.RuneLen(r) > limit {
			break
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}
func (s *Server) broadcast(m wire.Message) {
	s.mu.Lock()
	peers := make([]*peer, 0, len(s.peers))
	for p := range s.peers {
		peers = append(peers, p)
	}
	s.mu.Unlock()
	for _, p := range peers {
		if err := p.send(m); err != nil {
			p.conn.Close()
		}
	}
}
func (s *Server) download(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	req, err := wire.Read(c)
	if err != nil || req.Type != "get" {
		return
	}
	c.SetReadDeadline(time.Time{})
	f, item, err := s.shares.OpenByID(req.File.ID)
	if err != nil {
		wire.Write(c, wire.Message{Type: "error", Error: err.Error()})
		return
	}
	defer f.Close()
	if req.Offset < 0 || req.Offset > item.Size {
		wire.Write(c, wire.Message{Type: "error", Error: "invalid resume offset"})
		return
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return
	}
	st, err := f.Stat()
	if err != nil || st.Size() != item.Size || st.ModTime().UnixNano() != item.ModTime {
		wire.Write(c, wire.Message{Type: "error", Error: "file changed while preparing transfer"})
		return
	}
	if _, err := f.Seek(req.Offset, io.SeekStart); err != nil {
		return
	}
	c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if err := wire.Write(c, wire.Message{Type: "start", File: item, Offset: req.Offset, SHA256: hex.EncodeToString(hash.Sum(nil))}); err != nil {
		return
	}
	buf := make([]byte, 128*1024)
	remaining := item.Size - req.Offset
	for remaining > 0 {
		want := int64(len(buf))
		if remaining < want {
			want = remaining
		}
		n, err := io.ReadFull(f, buf[:int(want)])
		if err != nil {
			return
		}
		c.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if err := writeAll(c, buf[:n]); err != nil {
			return
		}
		remaining -= int64(n)
	}
	s.log(fmt.Sprintf("served %s (%d bytes)", item.Name, item.Size-req.Offset))
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
func guessLocalIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip != nil && ip.IsGlobalUnicast() {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}
