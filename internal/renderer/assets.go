package renderer

import (
	"bytes"
	"embed"
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

//go:embed assets/bg.png
//go:embed assets/thumb.png
//go:embed assets/Poppins-Regular.ttf
//go:embed assets/Poppins-Bold.ttf
var embeddedAssets embed.FS

// LoadBackgroundImage loads and scales the background image (from custom path or embedded asset)
func LoadBackgroundImage(runtimeConfig *config.RuntimeConfig) (*image.RGBA, error) {
	imagePath := runtimeConfig.GetBackgroundImagePath()

	var data []byte
	var err error

	// Check if using custom image path or embedded asset
	if runtimeConfig.BackgroundImagePath != "" {
		// Load from filesystem
		data, err = os.ReadFile(imagePath)
	} else {
		// Load from embedded assets
		data, err = embeddedAssets.ReadFile(imagePath)
	}

	if err != nil {
		return nil, err
	}

	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	// Convert to RGBA with proper scaling
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	// Use the fastest pure Go implementation according to speedtest-resize benchmarks
	draw.ApproxBiLinear.Scale(rgba, rgba.Bounds(), img, img.Bounds(), draw.Over, nil)

	return rgba, nil
}

// LoadFont loads the embedded TrueType font for video title overlay
func LoadFont(size float64) (font.Face, error) {
	fontBytes, err := embeddedAssets.ReadFile(config.VideoTitleFontAsset)
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
func DrawCenterText(img *image.RGBA, face font.Face, text string, centerY int, textColor color.RGBA) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textColor),
		Face: face,
	}

	// Measure text dimensions
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Calculate centered position (both horizontally and vertically)
	// Font baseline positioning means the Y coordinate is where the baseline sits.
	// To visually center text, we position baseline slightly below center.
	x := (config.Width - textWidth) / 2
	y := centerY + (textHeight / 3)

	d.Dot = freetype.Pt(x, y)
	d.DrawString(text)
}

// DrawEpisodeNumber draws the episode number in the top right corner
func DrawEpisodeNumber(img *image.RGBA, face font.Face, episodeNum string, textColor color.RGBA) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textColor),
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
