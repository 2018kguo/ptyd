package main

import (
	"fmt"
	"math"
	"strings"
	"time"
)

const workspaceMotionInterval = 200 * time.Millisecond

func workspaceBrandReserve(cols int) int {
	if cols >= 112 {
		return 12
	}
	return 0
}

// The brand's small wireframe cube is decorative. It never delays navigation,
// and animation updates touch only this header rectangle.
func workspaceBrand(cols, rows, phase int, color bool) string {
	if cols < 40 || rows < 16 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\x1b7")
	if color {
		b.WriteString("\x1b[36m")
	}
	if cols < 112 {
		fmt.Fprintf(&b, "\x1b[2;1H%c", []byte{'/', '-', '\\', '|'}[(phase/2)%4])
	} else {
		frame := workspaceCube(phase)
		for y, line := range frame {
			fmt.Fprintf(&b, "\x1b[%d;%dH%s", y+1, cols-10, line)
		}
	}
	b.WriteString("\x1b[0m\x1b8")
	return b.String()
}
func workspaceCube(phase int) []string {
	const width, height = 9, 4
	points := [8][2]int{}
	angle := float64(phase%32) * math.Pi / 16
	for i := range points {
		x, y, z := -1., -1., -1.
		if i&1 != 0 {
			x = 1
		}
		if i&2 != 0 {
			y = 1
		}
		if i&4 != 0 {
			z = 1
		}
		rx, rz := x*math.Cos(angle)+z*math.Sin(angle), -x*math.Sin(angle)+z*math.Cos(angle)
		ry := y*math.Cos(.45) - rz*math.Sin(.45)
		points[i] = [2]int{min(max(int(math.Round(4+2.3*rx)), 0), width-1), min(max(int(math.Round(1.5+.95*ry)), 0), height-1)}
	}
	grid := [height][width]byte{}
	for y := range grid {
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}
	for i, a := range points {
		for _, bit := range []int{1, 2, 4} {
			if i&bit != 0 {
				continue
			}
			c := points[i|bit]
			dx, dy := c[0]-a[0], c[1]-a[1]
			steps := max(absWorkspace(dx), absWorkspace(dy))
			mark := byte('-')
			if dx == 0 {
				mark = '|'
			} else if dy != 0 {
				if dx*dy > 0 {
					mark = '\\'
				} else {
					mark = '/'
				}
			}
			for k := 0; k <= steps; k++ {
				t := 0.
				if steps > 0 {
					t = float64(k) / float64(steps)
				}
				x, y := int(math.Round(float64(a[0])+float64(dx)*t)), int(math.Round(float64(a[1])+float64(dy)*t))
				grid[y][x] = mark
			}
		}
	}
	for _, p := range points {
		grid[p[1]][p[0]] = '+'
	}
	out := make([]string, height)
	for y := range grid {
		out[y] = string(grid[y][:])
	}
	return out
}
func absWorkspace(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
