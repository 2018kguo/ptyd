package client

import (
	"fmt"
	"github.com/2018kguo/phosphor"
	"testing"
)

func TestIdentityAccentsStayStableAndAdaptToTheme(t *testing.T) {
	dark, light := phosphor.RGB{R: 40, G: 44, B: 52}, phosphor.RGB{R: 250, G: 250, B: 250}
	seen := map[phosphor.RGB]bool{}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("term-%d", i)
		a := IdentityColor(id, dark)
		b := IdentityColor(id, light)
		seen[a] = true
		if a != IdentityColor(id, dark) || a == b {
			t.Fatal("identity changed or ignored light theme")
		}
		if min(a.R, a.G, a.B) < 130 || max(b.R, b.G, b.B) > 115 {
			t.Fatal("accent brightness does not adapt to theme")
		}
	}
	if len(seen) < 15 {
		t.Fatalf("identities collapsed into %d colors", len(seen))
	}
}
