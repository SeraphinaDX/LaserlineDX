package tui

import (
	"image"

	ui "github.com/metaspartan/gotui/v5"
)

// styleWarpPanel supplies an opaque background to *every* part of a gotui
// panel. Setting BackgroundColor alone only fills blank cells: the widgets
// replace painted cells with their independent text, border and title styles.
// Transparent replacements reveal the terminal's own colour (e.g. Alacritty).
func styleWarpPanel(b *ui.Block, border ui.Color) {
	b.BackgroundColor = warpInk
	b.FillBorder = true
	b.BorderStyle = ui.NewStyle(border, warpInk)
	b.TitleStyle = ui.NewStyle(warpGold, warpInk)
	b.TitleBottomStyle = ui.NewStyle(warpGold, warpInk)
}

// warpBackdrop paints the *entire* frame first. This also covers spare cells
// between panels, unused pane widths and cells exposed by resizing.
func warpBackdrop(width, height int) *ui.Block {
	b := ui.NewBlock()
	b.Border = false
	b.BackgroundColor = warpInk
	b.SetRect(0, 0, width, height)
	return b
}

// warpStatusLine avoids the implicit two-cell inset in gotui Paragraph. The
// footer's final one-row status area can now show its text without missing
// its own background.
type warpStatusLine struct {
	ui.Block
	text string
}

func warpStatus(message string, width, height int) *warpStatusLine {
	s := &warpStatusLine{text: message}
	s.Border = false
	s.SetRect(0, height-1, width, height)
	return s
}

func (s *warpStatusLine) Draw(buf *ui.Buffer) {
	r := s.GetRect()
	style := ui.NewStyle(warpGold, warpInk)
	buf.Fill(ui.NewCell(' ', style), r)
	if r.Dx() > 0 && r.Dy() > 0 {
		buf.SetString(fitLine(" "+s.text, r.Dx()), style, image.Pt(r.Min.X, r.Min.Y))
	}
}
