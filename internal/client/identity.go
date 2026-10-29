package client

import (
	"fmt"
	"hash/fnv"

	"github.com/2018kguo/phosphor"
)

// IdentityColor varies the hue with the immutable id, so names, navigation and
// restarts keep the same accent without another stored setting. Light themes
// use darker accents. Text and icons still identify every state without color.
func IdentityColor(id string, background phosphor.RGB) phosphor.RGB {
	hash := fnv.New32a()
	hash.Write([]byte(id))
	hue := int(hash.Sum32() % 360)
	low, high := 130, 220
	if 299*int(background.R)+587*int(background.G)+114*int(background.B) >= 128000 {
		low, high = 35, 115
	}
	rising := low + (high-low)*(hue%60)/60
	falling := high - (high-low)*(hue%60)/60
	var r, g, b int
	switch hue / 60 {
	case 0:
		r, g, b = high, rising, low
	case 1:
		r, g, b = falling, high, low
	case 2:
		r, g, b = low, high, rising
	case 3:
		r, g, b = low, falling, high
	case 4:
		r, g, b = rising, low, high
	case 5:
		r, g, b = high, low, falling
	}
	return phosphor.RGB{R: uint8(r), G: uint8(g), B: uint8(b)}
}
func IdentityStyle(id string, background phosphor.RGB) string {
	c := IdentityColor(id, background)
	return fmt.Sprintf("38;2;%d;%d;%d", c.R, c.G, c.B)
}
