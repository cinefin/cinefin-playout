package server

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Drawing helpers for the overlay's scenes. Everything is ASS events on the
// 1280x720 canvas that overlayFrame declares; mpv scales it to the screen.
// Positions are canvas pixels from the top-left corner.

// assColour turns a CSS "#rrggbb" colour into ASS's "&HBBGGRR&".
func assColour(hex string) string {
	h := strings.TrimPrefix(hex, "#")
	if len(h) != 6 {
		return "&HFFFFFF&"
	}
	return "&H" + strings.ToUpper(h[4:6]+h[2:4]+h[0:2]) + "&"
}

// assAlpha turns an opacity (1 = opaque, 0 = invisible) into ASS's alpha
// "&HAA&", where 00 is opaque and FF invisible.
func assAlpha(opacity float64) string {
	opacity = min(1, max(0, opacity))
	return fmt.Sprintf("&H%02X&", 255-int(opacity*255))
}

// fade is the override tag that draws text at the given opacity, or nothing
// when it is fully opaque (text is opaque by default).
func fade(opacity float64) string {
	if opacity >= 1 {
		return ""
	}
	return `\alpha` + assAlpha(opacity)
}

// mix blends two "#rrggbb" colours: p = 0 gives a, 1 gives b.
func mix(a, b string, p float64) string {
	if p <= 0 {
		return a
	}
	if p >= 1 {
		return b
	}
	ca, _ := strconv.ParseUint(strings.TrimPrefix(a, "#"), 16, 32)
	cb, _ := strconv.ParseUint(strings.TrimPrefix(b, "#"), 16, 32)
	var out uint64
	for _, shift := range []uint{16, 8, 0} {
		x, y := float64(ca>>shift&0xff), float64(cb>>shift&0xff)
		out |= uint64(math.Round(x+(y-x)*p)) << shift
	}
	return fmt.Sprintf("#%06x", out)
}

// rectPath is the ASS drawing path of a w x h rectangle at the drawing origin.
func rectPath(w, h int) string {
	return fmt.Sprintf("m 0 0 l %d 0 %d %d 0 %d", w, w, h, h)
}

// shape is one ASS vector drawing (path in drawing units, see rectPath) with
// its top-left corner at (x, y), in colour ("#rrggbb") at the given opacity.
func shape(x, y int, colour string, opacity float64, path string) string {
	return fmt.Sprintf(`{\an7\pos(%d,%d)\bord0\shad0\1c%s\1a%s\p1}%s{\p0}`,
		x, y, assColour(colour), assAlpha(opacity), path)
}

// rect is a filled rectangle.
func rect(x, y, w, h int, colour string, opacity float64) string {
	return shape(x, y, colour, opacity, rectPath(w, h))
}

// A 1 px line on the 720-line canvas would be 1.5 px, and blurred, on a 1080p
// screen. Hairlines are drawn in 1080p pixels instead: the drawing is scaled
// by 720/1080, so one unit is one pixel on a 1080p screen (two on 4K).
const hairScale = `\fscx66.6667\fscy66.6667`

// hairShape is shape for a path in 1080p pixels (see hairScale).
func hairShape(x, y int, colour string, opacity float64, path string) string {
	return fmt.Sprintf(`{\an7\pos(%d,%d)\bord0\shad0%s\1c%s\1a%s\p1}%s{\p0}`,
		x, y, hairScale, assColour(colour), assAlpha(opacity), path)
}

// border is a hairline outline just inside the edge of a w x h rectangle (in
// canvas pixels): the outer rectangle with the inner one wound the other way,
// which cuts it out.
func border(x, y, w, h int, colour string, opacity float64) string {
	w, h = w*3/2, h*3/2
	in := fmt.Sprintf(" m 1 1 l 1 %d %d %d %d 1", h-1, w-1, h-1, w-1)
	return hairShape(x, y, colour, opacity, rectPath(w, h)+in)
}

// vline is a vertical hairline h canvas pixels long, from (x, y) down.
func vline(x, y, h int, colour string, opacity float64) string {
	return hairShape(x, y, colour, opacity, rectPath(1, h*3/2))
}

// textAt is text with its alignment point at (x, y): an is the ASS numpad
// alignment (7 top-left, 8 top-centre, 4 middle-left, ...). tags are extra
// override tags such as `\b1`, `\fsp2` or fade(0.5). The text is not escaped:
// pass untrusted parts through assText. Lines break only at \N.
func textAt(an, x, y, size int, colour, tags, text string) string {
	return fmt.Sprintf(`{\an%d\pos(%d,%d)\q2\bord0\shad0\fs%d\1c%s%s}%s`,
		an, x, y, size, assColour(colour), tags, text)
}
