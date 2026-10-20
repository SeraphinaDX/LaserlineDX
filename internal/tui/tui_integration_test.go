package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"warp/internal/hub"
	"warp/internal/invite"
	"warp/internal/transfer"
	"warp/internal/wire"
)

func TestBatchDownloadsMultipleFlaggedFiles(t *testing.T) {
	shared := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt", "skip.txt"} {
		if err := os.WriteFile(filepath.Join(shared, name), []byte("hello "+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	srv, err := hub.Start(hub.Options{Path: shared, Listen: "127.0.0.1:0", Advertise: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	code, err := invite.Parse(srv.Invitation())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, welcome, err := transfer.Join(ctx, code, "batch tester")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dest := t.TempDir()
	a := &bbs{ctx: ctx, client: client, files: welcome.Files, flagged: map[string]bool{},
		downloads: dest, progress: make(chan transfer.Progress, 32), completed: make(chan batchResult, 1)}
	for _, f := range a.files {
		if f.Name != "skip.txt" {
			a.flagged[f.ID] = true
		}
	}
	a.downloadFlagged()
	select {
	case result := <-a.completed:
		if result.err != nil || len(result.succeeded) != 2 || result.total != 2 {
			t.Fatalf("failed to download both flagged files: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("timed out downloading flagged files")
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		content, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(content) != "hello "+name {
			t.Fatalf("wrong download of %s: %q %v", name, content, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "skip.txt")); !os.IsNotExist(err) {
		t.Fatalf("unflagged file was downloaded: %v", err)
	}
}
