package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

	"laserline/internal/transfer"
	"laserline/internal/wire"
)

func testBBS(count, width, height int) *bbs {
	a := &bbs{flagged: make(map[string]bool), input: widgets.NewInput(),
		layout: makeLayout(width, height, false)}
	for i := 0; i < count; i++ {
		a.files = append(a.files, wire.File{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("file%04d.txt", i), Size: int64(i)})
	}
	a.rebuildEntries("")
	return a
}

func TestLargeListViewportAndKeyboard(t *testing.T) {
	a := testBBS(2500, 120, 30) // file viewport accounts for the status panel
	for i := 0; i < 999; i++ {
		a.key("<Down>")
	}
	if a.cursor != 999 || a.scroll != 999-a.layout.fileRows+1 {
		t.Fatalf("cursor and viewport diverged: cursor=%d scroll=%d rows=%d", a.cursor, a.scroll, a.layout.fileRows)
	}
	a.key("<PageDown>")
	if a.cursor != 999+a.layout.fileRows-1 || a.cursor >= a.scroll+a.layout.fileRows {
		t.Fatalf("PageDown lost highlighted row: cursor=%d scroll=%d", a.cursor, a.scroll)
	}
	a.key("<End>")
	if a.cursor != 2499 || a.scroll != 2500-a.layout.fileRows {
		t.Fatalf("End did not reach final visible row: cursor=%d scroll=%d", a.cursor, a.scroll)
	}
	a.key("<Home>")
	if a.cursor != 0 || a.scroll != 0 {
		t.Fatalf("Home did not reset viewport: cursor=%d scroll=%d", a.cursor, a.scroll)
	}
	a.key("<PageUp>")
	if a.cursor != 0 || a.scroll != 0 {
		t.Fatal("PageUp went above the file list")
	}
}

func TestMouseHitTestingAndFlaggingScrolledFiles(t *testing.T) {
	a := testBBS(200, 120, 27)
	l := a.layout
	a.mouse("<MouseWheelDown>", 10, l.files.top+2)
	if a.scroll != 3 {
		t.Fatalf("wheel did not move viewport: %d", a.scroll)
	}
	y := l.files.top + 1 + 5
	a.handle(ui.Event{Type: ui.MouseEvent, ID: "<MouseRight>", Payload: ui.Mouse{X: 5, Y: y}})
	id := a.files[8].ID // scroll 3 + row 5
	if !a.flagged[id] || a.cursor != 8 {
		t.Fatalf("right-click flagged wrong file: cursor=%d flags=%v", a.cursor, a.flagged)
	}
	// Held mouse buttons must not repeatedly toggle the same flag.
	a.mouse("<MouseRight>", 5, y)
	if !a.flagged[id] {
		t.Fatal("held right-click unflagged file")
	}
	a.mouse("<MouseRelease>", 5, y)
	a.mouse("<MouseRight>", 5, y)
	if a.flagged[id] {
		t.Fatal("second right-click did not unflag file")
	}
	a.mouse("<MouseRelease>", 5, y)
	a.mouse("<MouseLeft>", 5, y+1)
	if a.cursor != 9 {
		t.Fatalf("left click did not select file: %d", a.cursor)
	}
	a.mouse("<MouseRelease>", 5, y+1)
	a.mouse("<MouseRight>", 1, l.files.top) // title/border isn't a row
	if len(a.flagged) != 0 {
		t.Fatal("title border click flagged an unseen file")
	}
}

func TestFlagQueueAndRefresh(t *testing.T) {
	a := testBBS(20, 80, 25)
	a.key("<Space>")
	a.key("<Down>")
	a.key(" ")
	a.key("<Down>")
	a.key("<Space>")
	if len(a.flagged) != 3 {
		t.Fatalf("expected 3 flags, got %d", len(a.flagged))
	}
	queue := a.queued()
	for i, entry := range queue {
		if entry.ID != a.files[i].ID {
			t.Fatalf("queued file order changed: %v", queue)
		}
	}
	original := append([]wire.File(nil), a.files...)
	// New list order changes, but the same stable IDs remain flagged.
	newOrder := append([]wire.File{original[19]}, original[:19]...)
	a.message(wire.Message{Type: "files", Files: newOrder})
	if a.cursor != 2 || a.entries[a.cursor].file.ID != "id-2" || len(a.flagged) != 3 {
		t.Fatalf("refresh lost selection or flags: cursor=%d flags=%v", a.cursor, a.flagged)
	}
	a.message(wire.Message{Type: "files", Files: append([]wire.File{newOrder[0]}, newOrder[2:]...)})
	if len(a.flagged) != 2 { // original id-0 dropped
		t.Fatalf("stale file flag was not removed: %v", a.flagged)
	}
}

func TestChatKeysAndMouseButtonsInNarrowLayout(t *testing.T) {
	a := testBBS(10, 50, 24)
	if a.key("<Tab>") || !a.composing {
		t.Fatal("Tab did not open chat")
	}
	a.layout = makeLayout(50, 24, true)
	a.key("F")
	if a.input.Text != "F" || len(a.flagged) != 0 {
		t.Fatal("F should type in chat, not flag files")
	}
	a.mouse("<MouseRight>", 6, a.layout.files.top+1)
	if len(a.flagged) != 0 {
		t.Fatal("hidden files were clickable while chat is shown")
	}
	a.key("<Escape>")
	if a.composing || a.input.Text != "" {
		t.Fatal("Escape did not close and clear chat prompt")
	}
	a.layout = makeLayout(50, 24, false)
	a.key("F")
	a.key("f")
	if len(a.flagged) != 0 {
		t.Fatal("F/f must not flag files after leaving chat")
	}
	a.key("<Space>")
	if !a.flagged[a.files[0].ID] {
		t.Fatal("Space did not flag after leaving chat")
	}
	b := a.layout.buttons[0]
	a.mouse("<MouseLeft>", b.left, b.top)
	if !strings.Contains(a.status, "No active connection") {
		t.Fatalf("Download All button did not initiate batch action: %s", a.status)
	}
	a.mouse("<MouseRelease>", b.left, b.top)
	b = a.layout.buttons[1]
	a.mouse("<MouseLeft>", b.left, b.top)
	if !a.composing {
		t.Fatal("Chat button did not open chat")
	}
	a.mouse("<MouseRelease>", b.left, b.top)
	b = a.layout.buttons[2]
	a.mouse("<MouseLeft>", b.left, b.top)
	if a.composing {
		t.Fatal("Files button did not exit chat")
	}
}

func TestEnterDownloadsFileWithoutFlagging(t *testing.T) {
	a := testBBS(3, 90, 28)
	if a.key("<Enter>") || a.composing {
		t.Fatal("Enter should download selected file, not enter chat")
	}
	if a.status != "No active connection" || len(a.flagged) != 0 {
		t.Fatalf("Enter did not initiate single file without flags: %q %+v", a.status, a.flagged)
	}
}

func TestFolderNavigationAndPreserveFlags(t *testing.T) {
	a := testBBS(0, 110, 29)
	a.files = []wire.File{
		{ID: "folder-a", Name: "alpha", IsDir: true},
		{ID: "folder-empty", Name: "empty", IsDir: true},
		{ID: "folder-nested", Name: "alpha/nested", IsDir: true},
		{ID: "a", Name: "alpha/first.bin", Size: 10},
		{ID: "b", Name: "alpha/nested/second.bin", Size: 20},
		{ID: "r", Name: "root.txt", Size: 4},
	}
	a.rebuildEntries("")
	if len(a.entries) != 3 || a.entries[0].file.Name != "alpha" ||
		a.entries[1].file.Name != "empty" || a.entries[2].file.Name != "root.txt" {
		t.Fatalf("root directories should be first: %+v", a.entries)
	}
	a.key("<Space>") // tag alpha recursively
	if !a.flaggedDirs["alpha"] {
		t.Fatal("directory was not tagged")
	}
	a.key("<Space>") // untag it to continue with standalone file tags
	if a.flaggedDirs["alpha"] {
		t.Fatal("directory did not untag")
	}
	a.key("<Enter>") // alpha
	if a.dir != "alpha" || len(a.entries) != 3 || !a.entries[0].parent ||
		a.entries[1].file.Name != "alpha/nested" || a.entries[2].file.Name != "alpha/first.bin" {
		t.Fatalf("alpha directory listing wrong: dir=%q entries=%+v", a.dir, a.entries)
	}
	a.key("<End>") // first.bin
	a.key("<Space>")
	if !a.flagged["a"] {
		t.Fatal("file in subfolder was not flagged")
	}
	a.key("<Home>")
	a.key("<Enter>") // .. -> root, highlights alpha
	if a.dir != "" || a.entries[a.cursor].file.Name != "alpha" {
		t.Fatalf("parent navigation lost folder selection: dir=%q cursor=%d", a.dir, a.cursor)
	}
	a.key("<Down>") // empty
	a.key("<Enter>")
	if a.dir != "empty" || len(a.entries) != 1 || !a.entries[0].parent {
		t.Fatalf("empty folder not navigable: dir=%q entries=%+v", a.dir, a.entries)
	}
	a.key("<Escape>") // also goes up in file mode
	if a.dir != "" || !a.flagged["a"] {
		t.Fatal("going back cleared the flags")
	}
	a.key("<Down>") // root.txt
	a.key("<Space>")
	queue := a.queued()
	if len(queue) != 2 || queue[0].Name != "alpha/first.bin" || queue[1].Name != "root.txt" {
		t.Fatalf("cross-folder batch queue incorrect: %+v", queue)
	}
	// Move into nested folder: parent Backspace should restore last folder.
	a.key("<Home>")
	a.key("<Enter>") // alpha
	a.key("<Enter>") // nested
	if a.dir != "alpha/nested" {
		t.Fatalf("nested Enter navigation failed: %s", a.dir)
	}
	a.key("<Backspace>")
	if a.dir != "alpha" || a.entries[a.cursor].file.Name != "alpha/nested" {
		t.Fatalf("Backspace did not restore folder selection: dir=%q", a.dir)
	}
}

func TestBrowserLargeNestedFolderScrollAndRefresh(t *testing.T) {
	a := testBBS(0, 120, 30)
	for i := 0; i < 1800; i++ {
		a.files = append(a.files, wire.File{ID: fmt.Sprint(i), Name: fmt.Sprintf("huge/file%04d.txt", i)})
	}
	a.rebuildEntries("")
	if len(a.entries) != 1 || !a.entries[0].file.IsDir {
		t.Fatal("nested directory was not synthesized")
	}
	a.key("<Enter>")
	a.key("<End>")
	if a.scroll != len(a.entries)-a.layout.fileRows || a.cursor != len(a.entries)-1 {
		t.Fatalf("scroll failed after entering huge directory: scroll=%d cursor=%d", a.scroll, a.cursor)
	}
	oldName := a.entries[a.cursor].file.Name
	a.message(wire.Message{Type: "files", Files: a.files})
	if a.entries[a.cursor].file.Name != oldName || a.dir != "huge" {
		t.Fatal("refresh lost nested-directory selection")
	}
	a.key("<Home>")
	if !a.entries[a.cursor].parent {
		t.Fatal("Home should select parent directory row")
	}
}

func TestSpaceOnlyFlagsFilesAndFIsUnbound(t *testing.T) {
	a := testBBS(2, 80, 25)
	a.key("f")
	a.key("F")
	if len(a.flagged) != 0 {
		t.Fatal("F/f must not flag a file")
	}
	a.key("<Space>")
	if !a.flagged[a.files[0].ID] {
		t.Fatal("Space did not flag file")
	}
	a.key(" ")
	if len(a.flagged) != 0 {
		t.Fatal("Space did not unflag file")
	}
}

func TestStatusPanelGeometryAcrossTerminalSizes(t *testing.T) {
	for _, dims := range [][2]int{{40, 16}, {50, 20}, {80, 24}, {120, 30}, {150, 40}} {
		l := makeLayout(dims[0], dims[1], false)
		if l.fileRows < 1 || l.files.bottom != l.transfer.top || l.transfer.bottom != l.prompt.top ||
			l.transfer.bottom-l.transfer.top < 3 {
			t.Fatalf("invalid geometry for %v: %+v", dims, l)
		}
		if l.files.bottom-l.files.top-2 != l.fileRows {
			t.Fatalf("viewport height mismatched file pane: %v, %+v", dims, l)
		}
		text := downloadPanelText(downloadStatus{active: true, name: "data.txt", count: 4, index: 2,
			bytes: 100, total: 200, batchBytes: 800, completedBytes: 200, message: "Receiving encrypted file"},
			l.transfer.right-l.transfer.left-2, l.transfer.bottom-l.transfer.top-2)
		if got := len(strings.Split(text, "\n")); got > l.transfer.bottom-l.transfer.top-2 {
			t.Fatalf("status pane too tall for %v: %d lines", dims, got)
		}
		for _, line := range strings.Split(text, "\n") {
			if len([]rune(line)) > l.transfer.right-l.transfer.left-2 {
				t.Fatalf("status line exceeds width for %v: %q", dims, line)
			}
		}
	}
}

func TestDownloadStatusProgressAndResumption(t *testing.T) {
	a := &bbs{download: downloadStatus{active: true, count: 3, batchBytes: 600}}
	start := time.Unix(123, 0)
	a.observeDownload(downloadEvent{Progress: transfer.Progress{Name: "folder/a.txt", Bytes: 50, Total: 200, Attempt: 1,
		Message: "Receiving encrypted file"}, index: 1}, start)
	a.observeDownload(downloadEvent{Progress: transfer.Progress{Name: "folder/a.txt", Bytes: 150, Total: 200, Attempt: 1,
		Message: "Receiving encrypted file"}, index: 1}, start.Add(time.Second))
	if a.download.speed != 100 || percentage(a.download.bytes, a.download.total) != 75 {
		t.Fatalf("incorrect current-file speed and percent: %+v", a.download)
	}
	if pct := percentage(a.download.completedBytes+a.download.bytes, a.download.batchBytes); pct != 25 {
		t.Fatalf("wrong batch percentage: %d", pct)
	}
	// The transfer layer reports Interrupted notices without Bytes. The
	// progress bar must not jump from 75%% to 0%% while reconnecting.
	a.observeDownload(downloadEvent{Progress: transfer.Progress{Name: "folder/a.txt", Total: 200, Attempt: 1,
		Message: "Interrupted: connection reset"}, index: 1}, start.Add(2*time.Second))
	if a.download.bytes != 150 || a.download.speed != 0 {
		t.Fatalf("interruption lost current-file progress: %+v", a.download)
	}
	// Resumed chunks count without duplicating the already downloaded prefix.
	a.observeDownload(downloadEvent{Progress: transfer.Progress{Name: "folder/a.txt", Bytes: 150, Total: 200, Attempt: 2,
		Message: "Receiving encrypted file"}, index: 1}, start.Add(3*time.Second))
	a.observeDownload(downloadEvent{Progress: transfer.Progress{Name: "folder/b.txt", Bytes: 50, Total: 200, Attempt: 1,
		Message: "Receiving encrypted file"}, index: 2, completedBytes: 200}, start.Add(4*time.Second))
	if a.download.speed != 0 || a.download.bytes != 50 || a.download.completedBytes != 200 {
		t.Fatalf("file transition did not reset speed and roll up bytes: %+v", a.download)
	}
	panel := downloadPanelText(a.download, 100, 5)
	for _, expected := range []string{"folder/b.txt", "Batch  41%", "File  25%", "attempt 1", "Receiving encrypted file"} {
		if !strings.Contains(panel, expected) {
			t.Errorf("missing %q in panel:\n%s", expected, panel)
		}
	}
}

func TestProgressBarClamping(t *testing.T) {
	if percentage(1000, 100) != 100 || percentage(-1, 100) != 0 {
		t.Fatal("percentage did not clamp")
	}
	if progressBar(50, 100, 10) != "[#####-----]" {
		t.Fatalf("wrong half-filled progress bar: %q", progressBar(50, 100, 10))
	}
}

func TestDoubleClickMouseNavigatesAndDownloads(t *testing.T) {
	a := testBBS(0, 120, 28)
	a.files = []wire.File{
		{Name: "folder", ID: "dir", IsDir: true},
		{Name: "folder/inside.txt", ID: "nested", Size: 8},
		{Name: "root.txt", ID: "root", Size: 12},
	}
	a.rebuildEntries("")
	l := a.layout
	when := time.Unix(200, 0)
	x, y := 5, l.files.top+1
	a.mouseAt("<MouseLeft>", x, y, when)
	if a.dir != "" {
		t.Fatal("single click should only select")
	}
	a.mouseAt("<MouseRelease>", x, y, when.Add(50*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y, when.Add(250*time.Millisecond))
	if a.dir != "folder" {
		t.Fatalf("double-click didn't open folder: %q", a.dir)
	}
	a.mouseAt("<MouseRelease>", x, y, when.Add(300*time.Millisecond))
	// row 0 is parent, row 1 is the nested file.
	a.mouseAt("<MouseLeft>", x, y+1, when.Add(time.Second))
	a.mouseAt("<MouseRelease>", x, y+1, when.Add(time.Second+50*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y+1, when.Add(time.Second+200*time.Millisecond))
	if a.status != "No active connection" {
		t.Fatalf("double-click file did not invoke the download path: %s", a.status)
	}
	if len(a.flagged) != 0 || len(a.flaggedDirs) != 0 {
		t.Fatal("double click should not tag anything")
	}
}

func TestDoubleClickDoesNotAccidentallyActivate(t *testing.T) {
	a := testBBS(3, 110, 30)
	l := a.layout
	first := time.Unix(1000, 0)
	x, y := 5, l.files.top+1
	a.mouseAt("<MouseLeft>", x, y, first)
	a.mouseAt("<MouseRelease>", x, y, first.Add(time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y+1, first.Add(90*time.Millisecond)) // changed row
	if a.status == "No active connection" {
		t.Fatal("different row activated")
	}
	a.mouseAt("<MouseRelease>", x, y+1, first.Add(100*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y+1, first.Add(time.Second)) // too slow
	if a.status == "No active connection" {
		t.Fatal("late click activated")
	}
	a.mouseAt("<MouseRelease>", x, y+1, first.Add(time.Second+time.Millisecond))
	a.mouseAt("<MouseRight>", x, y+1, first.Add(time.Second+30*time.Millisecond))
	if !a.flagged[a.files[1].ID] {
		t.Fatal("right click stopped tagging")
	}
	a.mouseAt("<MouseRelease>", x, y+1, first.Add(time.Second+35*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y+1, first.Add(time.Second+100*time.Millisecond))
	if a.status == "No active connection" {
		t.Fatal("right click did not cancel click pair")
	}
	a.mouseAt("<MouseRelease>", x, y+1, first.Add(time.Second+120*time.Millisecond))
	a.mouseAt("<MouseWheelDown>", x, y+1, first.Add(time.Second+130*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y+1, first.Add(time.Second+180*time.Millisecond))
	if a.status == "No active connection" {
		t.Fatal("scroll did not cancel click pair")
	}
}

func TestRecursiveTaggingDeduplicatesAndIncludesEmptyFolders(t *testing.T) {
	a := testBBS(0, 120, 30)
	a.files = []wire.File{
		{ID: "base", Name: "books", IsDir: true},
		{ID: "subdir", Name: "books/classics", IsDir: true},
		{ID: "empty", Name: "books/empty", IsDir: true},
		{ID: "book1", Name: "books/classics/one.txt", Size: 4},
		{ID: "book2", Name: "books/two.txt", Size: 9},
		{ID: "else", Name: "else.txt", Size: 3},
	}
	a.rebuildEntries("")
	a.key("<Space>")
	if !a.flaggedDirs["books"] {
		t.Fatal("space did not tag folder")
	}
	a.key("<Enter>")
	if !a.tagged(a.entries[1]) || !a.tagged(a.entries[2]) {
		t.Fatalf("subdirectories should visibly inherit tags: %+v", a.entries)
	}
	a.key("<End>")
	a.key("<Space>")
	if !strings.Contains(a.status, "tagged parent") {
		t.Fatalf("expected inherited-tag guidance: %s", a.status)
	}
	a.flagged["book1"] = true // independently flagged earlier file must appear just once
	q := a.queued()
	want := []string{"books", "books/classics", "books/classics/one.txt", "books/empty", "books/two.txt"}
	if len(q) != len(want) {
		t.Fatalf("wrong recursive expansion: %+v", q)
	}
	for i, f := range q {
		if f.Name != want[i] {
			t.Fatalf("queue[%d] = %q, expected %q", i, f.Name, want[i])
		}
	}
	// Untag from the root. The explicitly tagged file remains selected.
	a.key("<Backspace>")
	a.key("<Space>")
	q = a.queued()
	if len(q) != 1 || q[0].ID != "book1" {
		t.Fatalf("untagging folder erased independent tag: %+v", q)
	}
}

func TestRightClickTagsFoldersAndDoubleClickParent(t *testing.T) {
	a := testBBS(0, 110, 29)
	a.files = []wire.File{{Name: "dir", IsDir: true}, {ID: "file", Name: "dir/file"}}
	a.rebuildEntries("")
	x, y := 5, a.layout.files.top+1
	a.mouseAt("<MouseRight>", x, y, time.Unix(1, 0))
	if !a.flaggedDirs["dir"] {
		t.Fatal("right-click must tag directory")
	}
	a.mouseAt("<MouseRelease>", x, y, time.Unix(1, 0))
	a.mouseAt("<MouseLeft>", x, y, time.Unix(2, 0))
	a.mouseAt("<MouseRelease>", x, y, time.Unix(2, 0))
	a.mouseAt("<MouseLeft>", x, y, time.Unix(2, 0).Add(100*time.Millisecond))
	if a.dir != "dir" {
		t.Fatal("double click did not open tagged directory")
	}
	a.mouseAt("<MouseRelease>", x, y, time.Unix(2, 0).Add(150*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y, time.Unix(3, 0)) // parent
	a.mouseAt("<MouseRelease>", x, y, time.Unix(3, 0).Add(10*time.Millisecond))
	a.mouseAt("<MouseLeft>", x, y, time.Unix(3, 0).Add(150*time.Millisecond))
	if a.dir != "" {
		t.Fatalf("double-click parent failed, dir %q", a.dir)
	}
}
