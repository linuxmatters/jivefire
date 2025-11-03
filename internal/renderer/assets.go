package renderer

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/linuxmatters/visualizer-go/internal/config"
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

	bounds := img.Bounds()

	// Convert to RGBA
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	// If dimensions don't match, scale the image with bilinear interpolation
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		scaleX := float64(bounds.Dx()) / float64(config.Width)
		scaleY := float64(bounds.Dy()) / float64(config.Height)

		for y := 0; y < config.Height; y++ {
			for x := 0; x < config.Width; x++ {
				// Bilinear interpolation
				srcX := float64(x) * scaleX
				srcY := float64(y) * scaleY

				x0 := int(srcX)
				y0 := int(srcY)
				x1 := x0 + 1
				y1 := y0 + 1

				// Clamp to image bounds
				if x1 >= bounds.Dx() {
					x1 = bounds.Dx() - 1
				}
				if y1 >= bounds.Dy() {
					y1 = bounds.Dy() - 1
				}

				// Get the four surrounding pixels
				c00 := img.At(x0, y0)
				c10 := img.At(x1, y0)
				c01 := img.At(x0, y1)
				c11 := img.At(x1, y1)

				// Calculate interpolation weights
				fx := srcX - float64(x0)
				fy := srcY - float64(y0)

				// Convert to RGBA for arithmetic
				r00, g00, b00, a00 := c00.RGBA()
				r10, g10, b10, a10 := c10.RGBA()
				r01, g01, b01, a01 := c01.RGBA()
				r11, g11, b11, a11 := c11.RGBA()

				// Bilinear interpolation formula
				r := uint8((float64(r00>>8)*(1-fx)*(1-fy) +
					float64(r10>>8)*fx*(1-fy) +
					float64(r01>>8)*(1-fx)*fy +
					float64(r11>>8)*fx*fy))

				g := uint8((float64(g00>>8)*(1-fx)*(1-fy) +
					float64(g10>>8)*fx*(1-fy) +
					float64(g01>>8)*(1-fx)*fy +
					float64(g11>>8)*fx*fy))

				b := uint8((float64(b00>>8)*(1-fx)*(1-fy) +
					float64(b10>>8)*fx*(1-fy) +
					float64(b01>>8)*(1-fx)*fy +
					float64(b11>>8)*fx*fy))

				a := uint8((float64(a00>>8)*(1-fx)*(1-fy) +
					float64(a10>>8)*fx*(1-fy) +
					float64(a01>>8)*(1-fx)*fy +
					float64(a11>>8)*fx*fy))

				rgba.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
			}
		}
	} else {
		// Direct copy if dimensions match
		draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	}

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
