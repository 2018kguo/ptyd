package main

import (
	"testing"

	"github.com/2018kguo/phosphor"
)

func TestWorkspaceAnimationTouchesOnlyBrand(t *testing.T) {
	for _, cols := range []int{40, 80, 120, 200} {
		const rows = 30
		host := phosphor.New(cols, rows)
		host.Write([]byte(workspaceFrame(workspaceView{}, cols, rows)))
		before := phosphor.New(cols, rows)
		if err := before.RestoreSnapshot(host.Snapshot(), phosphor.SnapshotLimits{}); err != nil {
			t.Fatal(err)
		}
		host.Write([]byte(workspaceBrand(cols, rows, 9, true)))
		changed := false
		for y := 0; y < rows; y++ {
			for x := 0; x < cols; x++ {
				mark := y == 1 && x == 0
				if cols >= 112 {
					mark = y < 4 && x >= cols-11 && x < cols-2
				}
				if host.Cell(x, y) != before.Cell(x, y) {
					if !mark {
						t.Fatalf("animation changed content at %d,%d on %d columns", x, y, cols)
					}
					changed = true
				}
			}
		}
		if !changed {
			t.Fatal("brand did not animate")
		}
	}
	for _, icon := range []rune{'●', '◉', '◇', '◆', '✓', '□'} {
		if phosphor.CodepointWidth(icon) != 1 {
			t.Fatalf("status icon %c has unstable width", icon)
		}
	}
}
