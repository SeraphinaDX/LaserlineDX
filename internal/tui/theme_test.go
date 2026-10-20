package tui

import (
	"image"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
)

func TestWarpBackgroundCoversWholeTerminal(t *testing.T) {
	const width, height = 92, 27
	buf := ui.NewBuffer(image.Rect(0, 0, width, height))
	warpBackdrop(width, height).Draw(buf)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := buf.GetCell(image.Pt(x, y))
			if c.Style.Bg != warpInk {
				t.Fatalf("unpainted cell at (%d,%d): %+v", x, y, c)
			}
		}
	}
}

func TestWarpPanelStylesHaveOpaqueBackground(t *testing.T) {
	b := ui.NewBlock()
	styleWarpPanel(b, warpCyan)
	if b.BackgroundColor != warpInk || !b.FillBorder ||
		b.BorderStyle.Bg != warpInk || b.TitleStyle.Bg != warpInk ||
		b.TitleBottomStyle.Bg != warpInk {
		t.Fatalf("panel lets terminal background show through: %+v", b)
	}
}

func TestWarpStatusPaintsItsOwnRow(t *testing.T) {
	buf := ui.NewBuffer(image.Rect(0, 0, 48, 16))
	s := warpStatus("Connected to Warp", 48, 16)
	s.Draw(buf)
	for x := 0; x < 48; x++ {
		if cell := buf.GetCell(image.Pt(x, 15)); cell.Style.Bg != warpInk {
			t.Fatalf("status cell %d transparent: %+v", x, cell)
		}
	}
	if c := buf.GetCell(image.Pt(1, 15)); c.Rune != 'C' {
		t.Fatalf("status missing text: %+v", c)
	}
}
