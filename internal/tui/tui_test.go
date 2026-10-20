package tui

import (
	"fmt"
	"strings"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

	"warp/internal/wire"
)

func testBBS(count, width, height int) *bbs {
	a := &bbs{flagged: make(map[string]bool), input: widgets.NewInput(),
		layout: makeLayout(width, height, false)}
	for i := 0; i < count; i++ {
		a.files = append(a.files, wire.File{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("dir/file%04d.txt", i), Size: int64(i)})
	}
	return a
}

func TestLargeListViewportAndKeyboard(t *testing.T) {
	a := testBBS(2500, 120, 30) // 18 visible file rows
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
	a.key("F")
	a.key("<Down>")
	a.key("f")
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
	if a.cursor != 3 || len(a.flagged) != 3 {
		t.Fatalf("refresh lost selection or flags: cursor=%d flags=%v", a.cursor, a.flagged)
	}
	a.message(wire.Message{Type: "files", Files: append([]wire.File{newOrder[0]}, newOrder[2:]...)})
	if len(a.flagged) != 2 { // original id-0 dropped
		t.Fatalf("stale file flag was not removed: %v", a.flagged)
	}
}

func TestChatKeysAndMouseButtonsInNarrowLayout(t *testing.T) {
	a := testBBS(10, 50, 24)
	if a.key("<Enter>") || !a.composing {
		t.Fatal("Enter did not open chat")
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
	if !a.flagged[a.files[0].ID] {
		t.Fatal("F did not work after leaving chat")
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
