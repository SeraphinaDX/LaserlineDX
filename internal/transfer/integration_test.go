package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"warp/internal/hub"
	"warp/internal/invite"
	"warp/internal/wire"
)

func startTestHost(t *testing.T, data []byte) (*hub.Server, invite.Code) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := hub.Start(hub.Options{Path: dir, Listen: "127.0.0.1:0", Advertise: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	code, err := invite.Parse(srv.Invitation())
	if err != nil {
		t.Fatal(err)
	}
	return srv, code
}
func TestEncryptedChatToMultipleUsers(t *testing.T) {
	_, code := startTestHost(t, []byte("hello"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	alice, aw, err := Join(ctx, code, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	bob, bw, err := Join(ctx, code, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	if aw.Name != "Alice" || bw.Name != "Bob" || len(bw.Files) != 1 {
		t.Fatalf("incorrect welcomes: %+v %+v", aw, bw)
	}
	if err := alice.Send(wire.Message{Type: "chat", Text: "private hello"}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case m := <-bob.Updates:
			if m.Type == "chat" {
				if m.Name != "Alice" || m.Text != "private hello" {
					t.Fatalf("wrong broadcast: %+v", m)
				}
				return
			}
		case <-timer.C:
			t.Fatal("timed out waiting for encrypted chat broadcast")
		}
	}
}
func TestResumeAndVerify(t *testing.T) {
	data := make([]byte, 512*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	_, code := startTestHost(t, data)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, wel, err := Join(ctx, code, "tester")
	if err != nil {
		t.Fatal(err)
	}
	client.Close()
	entry := wel.Files[0]
	out := t.TempDir()
	path := filepath.Join(out, entry.Name)
	if err := os.WriteFile(path+".warp-part", data[:120000], 0600); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(manifest{ID: entry.ID, Name: entry.Name, Size: entry.Size})
	if err := os.WriteFile(path+".warp-part.json", meta, 0600); err != nil {
		t.Fatal(err)
	}
	if err := Download(ctx, code, entry, out, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("resumed file contents mismatch")
	}
	if _, err := os.Stat(path + ".warp-part"); !os.IsNotExist(err) {
		t.Fatalf("partial file remains after completion: %v", err)
	}
	if err := Download(ctx, code, entry, out, func(Progress) {}); err == nil {
		t.Fatal("download overwritten existing complete file")
	}
}
func TestWrongTLSFingerprintRejected(t *testing.T) {
	_, code := startTestHost(t, []byte("abc"))
	code.Fingerprint = sha256.Sum256([]byte("untrusted"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := Join(ctx, code, "alice")
	if err == nil {
		t.Fatal("accepted wrong certificate")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung instead of rejecting fingerprint: %v", err)
	}
}
