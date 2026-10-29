package client

import (
	"github.com/2018kguo/phosphor"
	"testing"
)

func TestWithoutColorsRetainsConcealAndLayout(t *testing.T) {
	row := WithoutANSIColors([]byte("\x1b[2;3H\x1b[32;48;2;40;42;54;8mX\x1b[0m"))
	model := phosphor.New(10, 3)
	model.Write(row)
	cell := model.Cell(2, 1)
	if cell.Rune != 'X' || cell.Style.Fg != phosphor.DefaultColor || cell.Style.Bg != phosphor.DefaultColor || cell.Style.Attrs&phosphor.AttrHidden == 0 {
		t.Fatalf("no-color changed non-color state: %+v", cell)
	}
}
