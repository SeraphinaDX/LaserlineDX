package tui

import (
	"image"

	ui "github.com/metaspartan/gotui/v5"
)

// styleLaserlinePanel supplies an opaque background to *every* part of a gotui
// panel. Setting BackgroundColor alone only fills blank cells: the widgets
// replace painted cells with their independent text, border and title styles.
// Transparent replacements reveal the terminal's own colour (e.g. Alacritty).
func styleLaserlinePanel(b *ui.Block, border ui.Color) {
	b.BackgroundColor = laserlineInk
	b.FillBorder = true
	b.BorderStyle = ui.NewStyle(border, laserlineInk)
	b.TitleStyle = ui.NewStyle(laserlineGold, laserlineInk)
	b.TitleBottomStyle = ui.NewStyle(laserlineGold, laserlineInk)
}

// laserlineBackdrop paints the *entire* frame first. This also covers spare cells
// between panels, unused pane widths and cells exposed by resizing.
func laserlineBackdrop(width, height int) *ui.Block {
	b := ui.NewBlock()
	b.Border = false
	b.BackgroundColor = laserlineInk
	b.SetRect(0, 0, width, height)
	return b
}

// laserlineStatusLine avoids the implicit two-cell inset in gotui Paragraph. The
// footer's final one-row status area can now show its text without missing
// its own background.
type laserlineStatusLine struct {
	ui.Block
	text string
}

func laserlineStatus(message string, width, height int) *laserlineStatusLine {
	s := &laserlineStatusLine{text: message}
	s.Border = false
	s.SetRect(0, height-1, width, height)
	return s
}

func (s *laserlineStatusLine) Draw(buf *ui.Buffer) {
	r := s.GetRect()
	style := ui.NewStyle(laserlineGold, laserlineInk)
	buf.Fill(ui.NewCell(' ', style), r)
	if r.Dx() > 0 && r.Dy() > 0 {
		buf.SetString(fitLine(" "+s.text, r.Dx()), style, image.Pt(r.Min.X, r.Min.Y))
	}
}
