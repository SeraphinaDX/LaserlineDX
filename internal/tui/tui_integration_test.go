package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"laserline/internal/hub"
	"laserline/internal/invite"
	"laserline/internal/transfer"
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
		downloads: dest, progress: make(chan downloadEvent, 32), completed: make(chan batchResult, 1)}
	for _, f := range a.files {
		if f.Name != "skip.txt" {
			a.flagged[f.ID] = true
		}
	}
	a.downloadFlagged()
	if !a.download.active || a.download.count != 2 || a.download.batchBytes != int64(len("hello one.txt")+len("hello two.txt")) {
		t.Fatalf("batch status was not initialized: %+v", a.download)
	}
	select {
	case result := <-a.completed:
		if result.err != nil || len(result.succeeded) != 2 || result.total != 2 {
			t.Fatalf("failed to download both flagged files: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("timed out downloading flagged files")
	}
	// The transfer layer should report progress with batch positions so the
	// UI can show separate file and aggregate progress bars.
	seen := make(map[int]bool)
	for len(a.progress) != 0 {
		e := <-a.progress
		if e.index < 1 || e.index > 2 {
			t.Fatalf("invalid batch progress position: %+v", e)
		}
		seen[e.index] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("missing batch progress events: %v", seen)
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

func TestEnterInSubdirectoryDownloadsOnlySelectedFile(t *testing.T) {
	shared := t.TempDir()
	if err := os.Mkdir(filepath.Join(shared, "archive"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(shared, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "archive", "letter.txt"), []byte("nested encrypted file"), 0600); err != nil {
		t.Fatal(err)
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
	client, welcome, err := transfer.Join(ctx, code, "nested tester")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dest := t.TempDir()
	a := &bbs{ctx: ctx, client: client, files: welcome.Files, flagged: map[string]bool{},
		layout: makeLayout(110, 30, false), downloads: dest,
		progress: make(chan downloadEvent, 32), completed: make(chan batchResult, 1)}
	a.rebuildEntries("")
	if len(a.entries) != 2 || a.entries[0].file.Name != "archive" || a.entries[1].file.Name != "empty" {
		t.Fatalf("server failed to advertise both folders: %+v", a.entries)
	}
	a.key("<Enter>") // opens archive
	if a.dir != "archive" || a.composing || len(a.entries) != 2 {
		t.Fatalf("Enter didn't browse archive: dir=%q rows=%+v", a.dir, a.entries)
	}
	a.key("<Enter>") // selects letter.txt by default and downloads it
	if !a.busy || a.composing || a.download.count != 1 || len(a.flagged) != 0 {
		t.Fatalf("Enter didn't start unflagged single-file download: %+v", a.download)
	}
	select {
	case result := <-a.completed:
		if result.err != nil || len(result.succeeded) != 1 || result.lastName != "archive/letter.txt" {
			t.Fatalf("single-file download failed: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("timed out downloading nested file")
	}
	got, err := os.ReadFile(filepath.Join(dest, "archive", "letter.txt"))
	if err != nil || string(got) != "nested encrypted file" {
		t.Fatalf("downloaded nested file content wrong: %q %v", got, err)
	}
}

func TestTaggedFolderDownloadsFilesAndEmptyDirectories(t *testing.T) {
	shared := t.TempDir()
	for _, dir := range []string{"music", "music/albums", "music/empty"} {
		if err := os.MkdirAll(filepath.Join(shared, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shared, "music/albums/track.txt"), []byte("secure track"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shared, "root.txt"), []byte("not tagged"), 0600); err != nil {
		t.Fatal(err)
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
	client, welcome, err := transfer.Join(ctx, code, "directory tester")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	dest := t.TempDir()
	a := &bbs{ctx: ctx, client: client, files: welcome.Files, flagged: map[string]bool{}, flaggedDirs: map[string]bool{"music": true},
		downloads: dest, progress: make(chan downloadEvent, 32), completed: make(chan batchResult, 1)}
	a.downloadFlagged()
	select {
	case result := <-a.completed:
		if result.err != nil || result.filesDone != 1 || result.dirsDone != 3 || result.total != 4 {
			t.Fatalf("folder batch incomplete: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("timed out retrieving directory")
	}
	b, err := os.ReadFile(filepath.Join(dest, "music/albums/track.txt"))
	if err != nil || string(b) != "secure track" {
		t.Fatalf("nested download: %s, %v", b, err)
	}
	info, err := os.Stat(filepath.Join(dest, "music/empty"))
	if err != nil || !info.IsDir() {
		t.Fatalf("empty folder omitted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "root.txt")); !os.IsNotExist(err) {
		t.Fatal("untagged file included")
	}
}
