package tui

import (
	"image"
	"testing"

	ui "github.com/metaspartan/gotui/v5"
)

func TestLaserlineBackgroundCoversWholeTerminal(t *testing.T) {
	const width, height = 92, 27
	buf := ui.NewBuffer(image.Rect(0, 0, width, height))
	laserlineBackdrop(width, height).Draw(buf)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			c := buf.GetCell(image.Pt(x, y))
			if c.Style.Bg != laserlineInk {
				t.Fatalf("unpainted cell at (%d,%d): %+v", x, y, c)
			}
		}
	}
}

func TestLaserlinePanelStylesHaveOpaqueBackground(t *testing.T) {
	b := ui.NewBlock()
	styleLaserlinePanel(b, laserlineCyan)
	if b.BackgroundColor != laserlineInk || !b.FillBorder ||
		b.BorderStyle.Bg != laserlineInk || b.TitleStyle.Bg != laserlineInk ||
		b.TitleBottomStyle.Bg != laserlineInk {
		t.Fatalf("panel lets terminal background show through: %+v", b)
	}
}

func TestLaserlineStatusPaintsItsOwnRow(t *testing.T) {
	buf := ui.NewBuffer(image.Rect(0, 0, 48, 16))
	s := laserlineStatus("Connected to Laserline", 48, 16)
	s.Draw(buf)
	for x := 0; x < 48; x++ {
		if cell := buf.GetCell(image.Pt(x, 15)); cell.Style.Bg != laserlineInk {
			t.Fatalf("status cell %d transparent: %+v", x, cell)
		}
	}
	if c := buf.GetCell(image.Pt(1, 15)); c.Rune != 'C' {
		t.Fatalf("status missing text: %+v", c)
	}
}
