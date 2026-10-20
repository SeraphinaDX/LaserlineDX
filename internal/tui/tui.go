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

type bbs struct {
	files     []wire.File
	cursor    int
	chat      []string
	composing bool
	input     *widgets.Input
	nick      string
	people    int
	status    string
	progress  chan transfer.Progress
	completed chan error
	busy      bool
	ctx       context.Context
	client    *transfer.Client
	downloads string
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
	a := &bbs{ctx: ctx, client: c, files: welcome.Files, nick: welcome.Name, people: welcome.Users, status: "Connected securely (TLS 1.3)",
		downloads: downloads, progress: make(chan transfer.Progress, 32), completed: make(chan error, 1), input: widgets.NewInput()}
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
		case err := <-a.completed:
			a.busy = false
			if err != nil {
				a.status = "Download paused: " + err.Error()
			} else {
				a.status = "Download complete, SHA-256 verified"
			}
			a.draw()
		}
	}
}

func (a *bbs) message(m wire.Message) {
	switch m.Type {
	case "files":
		a.files = m.Files
		if a.cursor >= len(a.files) {
			a.cursor = 0
		}
		a.status = "File area refreshed"
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
	if a.composing {
		switch e.ID {
		case "<Escape>":
			a.composing = false
			a.input.Text = ""
			a.input.Cursor = 0
		case "<Enter>":
			body := strings.TrimSpace(a.input.Text)
			if body != "" {
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
			if utf8.RuneCountInString(e.ID) == 1 && !strings.HasPrefix(e.ID, "<") && len(a.input.Text) < 1000 {
				r, _ := utf8.DecodeRuneInString(e.ID)
				a.input.InsertRune(r)
			}
		}
		return false
	}
	switch e.ID {
	case "q", "<Escape>":
		return true
	case "c", "t", "<Tab>":
		a.composing = true
	case "j", "<Down>", "<MouseWheelDown>":
		if a.cursor < len(a.files)-1 {
			a.cursor++
		}
	case "k", "<Up>", "<MouseWheelUp>":
		if a.cursor > 0 {
			a.cursor--
		}
	case "r":
		if err := a.client.Send(wire.Message{Type: "list"}); err != nil {
			a.status = err.Error()
		}
	case "<Enter>", "d":
		a.getSelected()
	}
	return false
}
func (a *bbs) getSelected() {
	if a.busy {
		a.status = "A download is already in progress"
		return
	}
	if len(a.files) == 0 || a.cursor < 0 || a.cursor >= len(a.files) {
		a.status = "Nothing available to download"
		return
	}
	item := a.files[a.cursor]
	a.busy = true
	a.status = "Starting download of " + item.Name
	go func() {
		err := transfer.Download(a.ctx, a.client.Invitation, item, a.downloads, func(p transfer.Progress) {
			select {
			case a.progress <- p:
			default:
			}
		})
		select {
		case a.completed <- err:
		case <-a.ctx.Done():
		}
	}()
}
func (a *bbs) draw() {
	width, height := ui.TerminalDimensions()
	if width < 40 || height < 12 {
		ui.Clear()
		p := widgets.NewParagraph()
		p.Text = "Warp needs at least 40x12 terminal cells"
		p.SetRect(0, 0, max(width, 1), max(height, 1))
		ui.Render(p)
		return
	}
	header := widgets.NewParagraph()
	header.Title = " WARP // ENCRYPTED MINI BBS "
	header.Text = fmt.Sprintf("  * TLS 1.3   |   User: %s   |   Online: %d   |   Files: %d", a.nick, a.people, len(a.files))
	header.BorderStyle.Fg = ui.ColorPink
	header.TitleStyle.Fg = ui.ColorLightCyan
	header.SetRect(0, 0, width, 3)
	foot := widgets.NewParagraph()
	foot.BorderStyle.Fg = ui.ColorPink
	foot.Title = " COMMANDS "
	if a.composing {
		foot.Text = " Chat: type message, Enter sends, Esc returns to file navigation"
	} else {
		foot.Text = " j/k or arrows: select  |  Enter: download  |  c: chat  |  r: refresh  |  q: quit"
	}
	foot.SetRect(0, height-4, width, height-1)
	status := widgets.NewParagraph()
	status.Text = " " + a.status
	status.SetRect(0, height-1, width, height)
	status.TextStyle.Fg = ui.ColorLightCyan
	list := widgets.NewList()
	list.Title = " FILE AREA / Downloads "
	list.BorderStyle.Fg = ui.ColorLightCyan
	rows := make([]string, 0, len(a.files))
	for i, f := range a.files {
		marker := "  "
		if i == a.cursor {
			marker = "> "
		}
		rows = append(rows, fmt.Sprintf("%s%s  (%s)", marker, safe(f.Name), formatBytes(f.Size)))
	}
	if len(rows) == 0 {
		rows = []string{"  No shared files yet. Press r to refresh."}
	}
	list.Rows = rows
	list.WrapText = false
	chat := widgets.NewParagraph()
	chat.Title = " ENCRYPTED CHAT / Live "
	chat.BorderStyle.Fg = ui.ColorPink
	chat.TextStyle.Fg = ui.ColorWhite
	chat.WrapText = true
	chatText := a.chat
	// Only show newest messages that fit. The full buffer stays in memory.
	maxRows := max(1, height-12)
	if len(chatText) > maxRows {
		chatText = chatText[len(chatText)-maxRows:]
	}
	chat.Text = strings.Join(chatText, "\n")
	a.input.Title = " Message (Enter to send) "
	a.input.BorderStyle.Fg = ui.ColorPink
	a.input.SetRect(0, height-7, width, height-4)
	if width >= 92 {
		x := width * 45 / 100
		list.SetRect(0, 3, x, height-7)
		chat.SetRect(x, 3, width, height-7)
		ui.Clear()
		ui.Render(header, list, chat, a.input, foot, status)
	} else {
		list.SetRect(0, 3, width, height-7)
		chat.SetRect(0, 3, width, height-7)
		ui.Clear()
		if a.composing {
			ui.Render(header, chat, a.input, foot, status)
		} else {
			ui.Render(header, list, a.input, foot, status)
		}
	}
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
