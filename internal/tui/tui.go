// Package tui provides Warp's gotui v5 mini-BBS for terminal clients.
package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	ui "github.com/metaspartan/gotui/v5"
	"github.com/metaspartan/gotui/v5/widgets"

	"warp/internal/transfer"
	"warp/internal/wire"
)

const actionBar = " [ Download All ]  [ Chat ]  [ Files ]"

type area struct{ left, top, right, bottom int }

func (r area) contains(x, y int) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

type layout struct {
	files, chat, prompt area
	buttons             [3]area // Download All, Chat, Files
	fileRows            int
	filesVisible        bool
	wide                bool
}

// The same geometry powers both rendering and mouse hit testing. Each file
// occupies exactly one cell row; list widgets do not own the scroll position.
func makeLayout(width, height int, composing bool) layout {
	wide := width >= 92
	fileRight := width
	if wide {
		fileRight = width * 45 / 100
	}
	barY := height - 3 // content row inside the three-cell footer block
	l := layout{
		files:        area{0, 3, fileRight, height - 7},
		chat:         area{fileRight, 3, width, height - 7},
		prompt:       area{0, height - 7, width, height - 4},
		fileRows:     max(1, height-12),
		filesVisible: wide || !composing,
		wide:         wide,
	}
	if !wide {
		l.chat = area{0, 3, width, height - 7}
	}
	labels := [...]string{"[ Download All ]", "[ Chat ]", "[ Files ]"}
	for i, label := range labels {
		start := strings.Index(actionBar, label) + 1 // left border takes cell 0
		l.buttons[i] = area{start, barY, start + len(label), barY + 1}
	}
	return l
}

type batchResult struct {
	succeeded []string
	total     int
	failed    string
	err       error
}

type bbs struct {
	files     []wire.File
	cursor    int
	scroll    int
	flagged   map[string]bool // stable file ID, not row number
	chat      []string
	composing bool
	input     *widgets.Input
	nick      string
	people    int
	status    string
	progress  chan transfer.Progress
	completed chan batchResult
	busy      bool
	ctx       context.Context
	client    *transfer.Client
	downloads string
	layout    layout
	heldMouse string // avoid repeated toggles from held/dragged right-clicks
}

// Run owns the terminal and all widget rendering. Network goroutines send
// events to channels; they never manipulate gotui state directly.
func Run(ctx context.Context, c *transfer.Client, welcome wire.Message, downloads string) error {
	if err := ui.Init(); err != nil {
		return err
	}
	defer ui.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := &bbs{
		ctx: ctx, client: c, files: welcome.Files, nick: welcome.Name, people: welcome.Users,
		status:    "Connected securely (TLS 1.3). F flags files; D downloads flagged files.",
		downloads: downloads, progress: make(chan transfer.Progress, 32), completed: make(chan batchResult, 1),
		input: widgets.NewInput(), flagged: make(map[string]bool),
	}
	a.chat = append(a.chat, "Welcome to Warp! Your chat history is session-only.")
	a.draw()
	events := ui.PollEventsWithContext(ctx)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if a.handle(ev) {
				return nil
			}
			a.draw()
		case m, ok := <-c.Updates:
			if !ok {
				a.status = "Connection closed. Reconnect with the code; partial downloads can resume."
				a.draw()
				return nil
			}
			a.message(m)
			a.draw()
		case err := <-c.Done:
			if ctx.Err() == nil {
				a.status = "Disconnected: " + err.Error()
			}
			a.draw()
			return nil
		case p := <-a.progress:
			percent := 100
			if p.Total > 0 {
				percent = int(p.Bytes * 100 / p.Total)
			}
			a.status = fmt.Sprintf("%s - %d%% (%d/%d bytes) %s", p.Name, percent, p.Bytes, p.Total, p.Message)
			a.draw()
		case result := <-a.completed:
			// A buffered progress message from an earlier file must not
			// replace the batch's final success/failure summary.
			for len(a.progress) > 0 {
				<-a.progress
			}
			a.busy = false
			for _, id := range result.succeeded {
				delete(a.flagged, id)
			}
			if result.err != nil {
				a.status = fmt.Sprintf("Downloaded %d/%d; stopped at %s: %v. Failed/remaining files stay flagged.",
					len(result.succeeded), result.total, result.failed, result.err)
			} else {
				a.status = fmt.Sprintf("Downloaded %d file(s); SHA-256 verified.", result.total)
			}
			a.draw()
		}
	}
}

func (a *bbs) message(m wire.Message) {
	switch m.Type {
	case "files":
		oldID := ""
		if a.cursor >= 0 && a.cursor < len(a.files) {
			oldID = a.files[a.cursor].ID
		}
		a.files = m.Files
		live := make(map[string]bool, len(a.files))
		a.cursor = 0
		for i, f := range a.files {
			live[f.ID] = true
			if f.ID == oldID {
				a.cursor = i
			}
		}
		for id := range a.flagged {
			if !live[id] {
				delete(a.flagged, id)
			}
		}
		a.keepVisible()
		a.status = "File area refreshed (existing flags preserved for unchanged files)"
	case "notice":
		a.addLine("* " + m.Text)
		a.people = m.Users
	case "chat":
		a.addLine(m.Name + ": " + m.Text)
	case "error":
		a.status = "Server: " + m.Error
	}
}
func (a *bbs) addLine(s string) {
	// Prevent untrusted gotui formatting syntax from being interpreted.
	s = strings.NewReplacer("[", "(", "]", ")", "\x1b", "").Replace(s)
	a.chat = append(a.chat, s)
	if len(a.chat) > 200 {
		a.chat = a.chat[len(a.chat)-200:]
	}
}

func (a *bbs) handle(e ui.Event) bool {
	if e.ID == "<C-c>" {
		return true
	}
	if e.Type == ui.MouseEvent {
		if p, ok := e.Payload.(ui.Mouse); ok {
			a.mouse(e.ID, p.X, p.Y)
		}
		return false
	}
	return a.key(e.ID)
}

func (a *bbs) key(id string) bool {
	if a.composing {
		switch id {
		case "<Escape>":
			a.leaveChat()
		case "<Enter>":
			body := strings.TrimSpace(a.input.Text)
			if body != "" && a.client != nil {
				if err := a.client.Send(wire.Message{Type: "chat", Text: body}); err != nil {
					a.status = err.Error()
				}
			}
			a.input.Text = ""
			a.input.Cursor = 0
		case "<Backspace>":
			a.input.Backspace()
		case "<Left>":
			a.input.MoveCursorLeft()
		case "<Right>":
			a.input.MoveCursorRight()
		case "<Space>":
			a.input.InsertRune(' ')
		default:
			if utf8.RuneCountInString(id) == 1 && !strings.HasPrefix(id, "<") && len(a.input.Text) < 1000 {
				r, _ := utf8.DecodeRuneInString(id)
				a.input.InsertRune(r)
			}
		}
		return false
	}
	switch id {
	case "q":
		return true
	case "<Enter>", "c", "t", "<Tab>":
		a.composing = true
		a.status = "Chat active. Enter sends your message; Escape returns to the files."
	case "f", "F", "<Space>", " ":
		a.toggleFlag(a.cursor)
	case "d", "D":
		a.downloadFlagged()
	case "j", "<Down>":
		a.move(1)
	case "k", "<Up>":
		a.move(-1)
	case "<PageDown>":
		a.move(max(1, a.layout.fileRows-1))
	case "<PageUp>":
		a.move(-max(1, a.layout.fileRows-1))
	case "<Home>":
		a.cursor = 0
		a.keepVisible()
	case "<End>":
		a.cursor = max(0, len(a.files)-1)
		a.keepVisible()
	case "r":
		if a.client != nil {
			if err := a.client.Send(wire.Message{Type: "list"}); err != nil {
				a.status = err.Error()
			}
		}
	}
	return false
}

func (a *bbs) mouse(id string, x, y int) {
	if id == "<MouseRelease>" {
		a.heldMouse = ""
		return
	}
	if id == "<MouseLeft>" || id == "<MouseRight>" {
		if a.heldMouse == id {
			return
		}
		a.heldMouse = id
	}
	// Buttons remain usable even while typing in chat.
	if id == "<MouseLeft>" {
		for i, button := range a.layout.buttons {
			if button.contains(x, y) {
				switch i {
				case 0:
					a.downloadFlagged()
				case 1:
					a.composing = true
					a.status = "Chat active. Enter sends; Escape returns to files."
				case 2:
					a.leaveChat()
				}
				return
			}
		}
		if a.layout.prompt.contains(x, y) {
			a.composing = true
			return
		}
	}
	if !a.layout.filesVisible || !a.layout.files.contains(x, y) {
		return
	}
	if id == "<MouseWheelUp>" {
		a.scrollBy(-3)
		return
	}
	if id == "<MouseWheelDown>" {
		a.scrollBy(3)
		return
	}
	if id != "<MouseLeft>" && id != "<MouseRight>" {
		return
	}
	if x <= a.layout.files.left || x >= a.layout.files.right-1 ||
		y <= a.layout.files.top || y >= a.layout.files.bottom-1 {
		return // border/title, not a file
	}
	index := a.scroll + y - (a.layout.files.top + 1)
	if index < 0 || index >= len(a.files) {
		return
	}
	a.cursor = index
	if id == "<MouseRight>" {
		a.toggleFlag(index)
	}
}

func (a *bbs) leaveChat() {
	a.composing = false
	a.input.Text = ""
	a.input.Cursor = 0
}
func (a *bbs) move(amount int) {
	if len(a.files) == 0 {
		return
	}
	a.cursor = min(len(a.files)-1, max(0, a.cursor+amount))
	a.keepVisible()
}
func (a *bbs) keepVisible() {
	if len(a.files) == 0 {
		a.cursor, a.scroll = 0, 0
		return
	}
	rows := max(1, a.layout.fileRows)
	a.cursor = min(len(a.files)-1, max(0, a.cursor))
	if a.cursor < a.scroll {
		a.scroll = a.cursor
	} else if a.cursor >= a.scroll+rows {
		a.scroll = a.cursor - rows + 1
	}
	a.scroll = min(max(0, len(a.files)-rows), max(0, a.scroll))
}
func (a *bbs) scrollBy(n int) {
	rows := max(1, a.layout.fileRows)
	maxStart := max(0, len(a.files)-rows)
	a.scroll = min(maxStart, max(0, a.scroll+n))
	if len(a.files) > 0 {
		a.cursor = min(len(a.files)-1, max(a.scroll, min(a.cursor, a.scroll+rows-1)))
	}
}
func (a *bbs) toggleFlag(index int) {
	if index < 0 || index >= len(a.files) {
		a.status = "No file to flag"
		return
	}
	item := a.files[index]
	if a.flagged == nil {
		a.flagged = make(map[string]bool)
	}
	if a.flagged[item.ID] {
		delete(a.flagged, item.ID)
	} else {
		a.flagged[item.ID] = true
	}
	a.status = fmt.Sprintf("%d flagged. Press D or click Download All to start.", len(a.flagged))
}
func (a *bbs) queued() []wire.File {
	queue := make([]wire.File, 0, len(a.flagged))
	for _, f := range a.files {
		if a.flagged[f.ID] {
			queue = append(queue, f)
		}
	}
	return queue
}
func (a *bbs) downloadFlagged() {
	if a.busy {
		a.status = "A download batch is already in progress"
		return
	}
	queue := a.queued()
	if len(queue) == 0 {
		a.status = "Flag files using F or right-click first"
		return
	}
	if a.client == nil {
		a.status = "No active connection"
		return
	}
	a.busy = true
	a.status = fmt.Sprintf("Downloading %d flagged file(s) sequentially; interrupted files can resume.", len(queue))
	go func() {
		result := batchResult{total: len(queue)}
		for i, item := range queue {
			err := transfer.Download(a.ctx, a.client.Invitation, item, a.downloads, func(p transfer.Progress) {
				p.Message = fmt.Sprintf("[%d/%d] %s", i+1, len(queue), p.Message)
				select {
				case a.progress <- p:
				default:
				}
			})
			if err != nil {
				result.failed, result.err = item.Name, err
				break // preserve failed and remaining flags for retry
			}
			result.succeeded = append(result.succeeded, item.ID)
		}
		select {
		case a.completed <- result:
		case <-a.ctx.Done():
		}
	}()
}

func (a *bbs) draw() {
	width, height := ui.TerminalDimensions()
	if width < 40 || height < 16 {
		ui.Clear()
		p := widgets.NewParagraph()
		p.Text = "Warp needs at least 40x16 terminal cells"
		p.SetRect(0, 0, max(width, 1), max(height, 1))
		ui.Render(p)
		return
	}
	a.layout = makeLayout(width, height, a.composing)
	a.keepVisible()
	l := a.layout
	header := widgets.NewParagraph()
	header.Title = " WARP // ENCRYPTED MINI BBS "
	header.Text = fmt.Sprintf("  * TLS 1.3   |   User: %s   |   Online: %d   |   Files: %d   |   Flagged: %d",
		a.nick, a.people, len(a.files), len(a.flagged))
	header.BorderStyle.Fg = ui.ColorPink
	header.TitleStyle.Fg = ui.ColorLightCyan
	header.SetRect(0, 0, width, 3)

	foot := widgets.NewParagraph()
	foot.BorderStyle.Fg = ui.ColorPink
	foot.Title = " F: flag  D: download all  Enter: chat  Esc: files  PgUp/PgDn: scroll  q: quit "
	foot.Text = actionBar
	foot.TextStyle.Fg = ui.ColorLightCyan
	foot.SetRect(0, height-4, width, height-1)
	status := widgets.NewParagraph()
	status.Text = " " + a.status
	status.SetRect(0, height-1, width, height)
	status.TextStyle.Fg = ui.ColorLightCyan

	list := widgets.NewList()
	start := a.scroll
	end := min(len(a.files), start+l.fileRows)
	list.Title = fmt.Sprintf(" FILE AREA // %d-%d of %d | %d flagged ", visibleStart(start, end), end, len(a.files), len(a.flagged))
	list.BorderStyle.Fg = ui.ColorLightCyan
	list.WrapText = false
	list.Rows = make([]string, 0, max(1, end-start))
	for i := start; i < end; i++ {
		f := a.files[i]
		marker := "  "
		if i == a.cursor {
			marker = "> "
		}
		flag := "[ ]"
		if a.flagged[f.ID] {
			flag = "[x]"
		}
		list.Rows = append(list.Rows, fmt.Sprintf("%s%s %s  (%s)", marker, flag, safe(f.Name), formatBytes(f.Size)))
	}
	if len(list.Rows) == 0 {
		list.Rows = []string{"  No shared files yet. Press r to refresh."}
	}
	list.SelectedRow = max(0, a.cursor-a.scroll)
	list.SelectedStyle.Fg = ui.ColorLightCyan
	list.SetRect(l.files.left, l.files.top, l.files.right, l.files.bottom)

	chat := widgets.NewParagraph()
	chat.Title = " ENCRYPTED CHAT / Live "
	chat.BorderStyle.Fg = ui.ColorPink
	chat.TextStyle.Fg = ui.ColorWhite
	chat.WrapText = true
	chatText := a.chat
	maxRows := l.fileRows
	if len(chatText) > maxRows {
		chatText = chatText[len(chatText)-maxRows:]
	}
	chat.Text = strings.Join(chatText, "\n")
	chat.SetRect(l.chat.left, l.chat.top, l.chat.right, l.chat.bottom)

	var prompt ui.Drawable
	if a.composing {
		a.input.Title = " CHAT / Enter: send, Escape: files "
		a.input.BorderStyle.Fg = ui.ColorPink
		a.input.SetRect(l.prompt.left, l.prompt.top, l.prompt.right, l.prompt.bottom)
		prompt = a.input
	} else {
		p := widgets.NewParagraph()
		p.Title = " CHAT PROMPT "
		p.BorderStyle.Fg = ui.ColorPink
		p.Text = " Press Enter or click here to write a message."
		p.SetRect(l.prompt.left, l.prompt.top, l.prompt.right, l.prompt.bottom)
		prompt = p
	}
	ui.Clear()
	if l.wide {
		ui.Render(header, list, chat, prompt, foot, status)
	} else if a.composing {
		ui.Render(header, chat, prompt, foot, status)
	} else {
		ui.Render(header, list, prompt, foot, status)
	}
}
func visibleStart(start, end int) int {
	if end == 0 {
		return 0
	}
	return start + 1
}
func safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '[' {
			return '('
		}
		if r == ']' {
			return ')'
		}
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
}
func formatBytes(n int64) string {
	v := float64(n)
	for _, unit := range []string{"B", "KiB", "MiB", "GiB", "TiB"} {
		if v < 1024 || unit == "TiB" {
			return fmt.Sprintf("%.1f %s", v, unit)
		}
		v /= 1024
	}
	return "?"
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
