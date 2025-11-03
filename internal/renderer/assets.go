package renderer

import (
	"image"
	"image/color"
	"image/png"
	"os"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
)

// LoadBackgroundImage loads and scales a PNG background image
func LoadBackgroundImage(filename string) (*image.RGBA, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}

	// Convert to RGBA with proper scaling
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	// Use the fastest pure Go implementation according to speedtest-resize benchmarks
	draw.ApproxBiLinear.Scale(rgba, rgba.Bounds(), img, img.Bounds(), draw.Over, nil)

	return rgba, nil
}

// LoadFont loads a TrueType font from a file
func LoadFont(fontPath string, size float64) (font.Face, error) {
	fontBytes, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, err
	}

	f, err := truetype.Parse(fontBytes)
	if err != nil {
		return nil, err
	}

	face := truetype.NewFace(f, &truetype.Options{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})

	return face, nil
}

// DrawCenterText draws text centered horizontally at the specified Y position
func DrawCenterText(img *image.RGBA, face font.Face, text string, centerY int) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 248, G: 179, B: 29, A: 255}), // #F8B31D (brand yellow)
		Face: face,
	}

	// Measure text width
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()

	// Calculate centered position
	x := (config.Width - textWidth) / 2
	y := centerY + 10 // Slightly below center for better visual alignment

	d.Dot = freetype.Pt(x, y)
	d.DrawString(text)
}

// DrawEpisodeNumber draws the episode number in the top right corner
func DrawEpisodeNumber(img *image.RGBA, face font.Face, episodeNum string) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 248, G: 179, B: 29, A: 255}), // #F8B31D (brand yellow)
		Face: face,
	}

	// Measure text dimensions
	bounds, _ := d.BoundString(episodeNum)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Position in top right corner with proportional offset (40px from edges)
	offset := 30
	x := config.Width - textWidth - offset
	y := textHeight + offset

	d.Dot = freetype.Pt(x, y)
	d.DrawString(episodeNum)
}
