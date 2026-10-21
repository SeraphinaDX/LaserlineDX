package transfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"laserline/internal/hub"
	"laserline/internal/invite"
	"laserline/internal/wire"
)

func startOneTimeHost(t *testing.T, data []byte) (*hub.Server, invite.Code) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "example.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	srv, err := hub.Start(hub.Options{Path: dir, Listen: "127.0.0.1:0", Advertise: "127.0.0.1", OneTimePad: true})
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

func downloadGreeting(ctx context.Context, code invite.Code) (wire.Message, error) {
	conn, err := hub.Dial(ctx, code)
	if err != nil {
		return wire.Message{}, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := wire.Write(conn, wire.Message{Type: "hello", Role: "download", Token: hex.EncodeToString(code.Secret[:])}); err != nil {
		return wire.Message{}, err
	}
	return wire.Read(conn)
}

func TestOneTimePadRotatesAndKeepsExistingUsers(t *testing.T) {
	srv, first := startOneTimeHost(t, []byte("laserline test"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	alice, aw, err := Join(ctx, first, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	defer alice.Close()
	if aw.Token == "" || alice.Invitation.Secret == first.Secret {
		t.Fatal("first join did not receive distinct download credentials")
	}
	nextText := srv.Invitation()
	if nextText == first.String() {
		t.Fatal("invitation was not rotated after admission")
	}
	select {
	case <-srv.InvitationUpdates:
	case <-time.After(2 * time.Second):
		t.Fatal("console not notified of rotation")
	}
	if _, _, err := Join(ctx, first, "Reuser"); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("used code should be rejected: %v", err)
	}
	if msg, err := downloadGreeting(ctx, first); err != nil || msg.Type != "error" {
		t.Fatalf("old code allowed download: %+v, %v", msg, err)
	}
	next, err := invite.Parse(nextText)
	if err != nil {
		t.Fatal(err)
	}
	bob, welcome, err := Join(ctx, next, "Bob")
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()
	if welcome.Users != 2 {
		t.Fatalf("rotation disrupted multi-user room: got %d", welcome.Users)
	}
	if alice.Invitation.Secret == bob.Invitation.Secret {
		t.Fatal("both users received the same download credential")
	}
	if err := Download(ctx, alice.Invitation, aw.Files[0], t.TempDir(), func(Progress) {}); err != nil {
		t.Fatalf("first user's downloads broke on rotation: %v", err)
	}
	if err := alice.Send(wire.Message{Type: "chat", Text: "still connected"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(4 * time.Second)
	for {
		select {
		case m := <-bob.Updates:
			if m.Type == "chat" && m.Text == "still connected" {
				return
			}
		case <-deadline:
			t.Fatal("existing user's chat stopped on rotation")
		}
	}
}

func TestOneTimePadConcurrentReuseAdmitsExactlyOne(t *testing.T) {
	srv, first := startOneTimeHost(t, []byte("content"))
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []*Client
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client, _, err := Join(ctx, first, "Guest")
			if err == nil {
				mu.Lock()
				winners = append(winners, client)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	defer func() {
		for _, c := range winners { c.Close() }
	}()
	if len(winners) != 1 {
		t.Fatalf("one-use invitation admitted %d concurrent clients", len(winners))
	}
	if srv.Invitation() == first.String() {
		t.Fatal("code did not rotate")
	}
}

func TestOneTimePadRejectedDownloadDoesNotConsumeCode(t *testing.T) {
	srv, first := startOneTimeHost(t, []byte("data"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	msg, err := downloadGreeting(ctx, first)
	if err != nil || msg.Type != "error" {
		t.Fatalf("unadmitted download should fail: %+v, %v", msg, err)
	}
	if srv.Invitation() != first.String() {
		t.Fatal("failed download consumed invitation")
	}
}

func TestOneTimePadResumeAfterRejoiningWithFreshCode(t *testing.T) {
	data := make([]byte, 128*1024)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	srv, first := startOneTimeHost(t, data)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	alice, wel, err := Join(ctx, first, "Alice")
	if err != nil { t.Fatal(err) }
	alice.Close()
	next, err := invite.Parse(srv.Invitation())
	if err != nil { t.Fatal(err) }
	bob, _, err := Join(ctx, next, "Again")
	if err != nil { t.Fatal(err) }
	defer bob.Close()
	out := t.TempDir()
	entry := wel.Files[0]
	part := filepath.Join(out, entry.Name) + ".laserline-part"
	if err := os.WriteFile(part, data[:4096], 0600); err != nil { t.Fatal(err) }
	meta, _ := json.Marshal(manifest{ID: entry.ID, Name: entry.Name, Size: entry.Size})
	if err := os.WriteFile(part+".json", meta, 0600); err != nil { t.Fatal(err) }
	if err := Download(ctx, bob.Invitation, entry, out, func(Progress) {}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, entry.Name))
	if err != nil || !bytes.Equal(b, data) {
		t.Fatalf("resumed file mismatch: %v", err)
	}
}
