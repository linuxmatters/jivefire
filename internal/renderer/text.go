package renderer

import (
	"image"
	"image/color"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// newTextDrawer constructs a font.Drawer for drawing coloured text onto an RGBA image.
func newTextDrawer(img *image.RGBA, face font.Face, col color.RGBA) *font.Drawer {
	return &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(col),
		Face: face,
	}
}

// measureStringWidth returns the pixel width of text as rendered by the drawer.
func measureStringWidth(d *font.Drawer, text string) int {
	bounds, _ := d.BoundString(text)
	return (bounds.Max.X - bounds.Min.X).Ceil()
}

// measureTextBounds returns the pixel width and full bounds of text as rendered by the given face.
// Used when callers need both width and the vertical extent (ascent/descent).
func measureTextBounds(face font.Face, text string) (int, fixed.Rectangle26_6) {
	d := &font.Drawer{Face: face}
	bounds, _ := d.BoundString(text)
	width := (bounds.Max.X - bounds.Min.X).Ceil()
	return width, bounds
}
