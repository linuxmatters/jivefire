package config

import (
	"fmt"
	"strconv"
	"strings"
)

// Video settings
const (
	Width  = 1280
	Height = 720
	FPS    = 30
)

// Audio settings
const (
	SampleRate = 44100
	FFTSize    = 2048
)

// Visualization settings
const (
	NumBars      = 64   // Number of bars
	BarWidth     = 12   // Width of each bar
	BarGap       = 8    // Gap between bars
	CenterGap    = 100  // Gap between top and bottom bar sections
	MaxBarHeight = 0.50 // Maximum bar height as fraction of available space
)

// CAVA algorithm constants
const (
	Framerate      = 30.0
	NoiseReduction = 0.77  // CAVA default integral smoothing
	FallAccel      = 0.028 // CAVA gravity acceleration constant
)

// Appearance - Visual styling configuration
// Note: Future customization support will allow users to override these defaults.
// Embedded assets are currently located in internal/renderer/assets/
const (
	// Bar colors (RGB values for visualization bars)
	BarColorR = 164
	BarColorG = 0
	BarColorB = 0

	// Text/UI colors (RGB values for title overlay and framing lines)
	// Brand yellow #F8B31D - used for title text, framing lines, and thumbnail text
	TextColorR = 248
	TextColorG = 179
	TextColorB = 29

	// Embedded asset paths (relative to internal/renderer/assets/)
	// Background image: bg.png - scaled to video resolution (1280x720)
	// Thumbnail image: thumb.png - used as base for thumbnail generation
	BackgroundImageAsset = "assets/bg.png"
	ThumbnailImageAsset  = "assets/thumb.png"

	// Embedded font paths (relative to internal/renderer/assets/)
	// Video title font: Poppins-Regular.ttf - used for video overlay text
	// Thumbnail font: Poppins-Bold.ttf - used for thumbnail generation
	VideoTitleFontAsset = "assets/Poppins-Regular.ttf"
	ThumbnailFontAsset  = "assets/Poppins-Bold.ttf"

	// Thumbnail layout
	ThumbnailMargin              = 30  // Margin in pixels from edges for thumbnail text
	ThumbnailTextRotationDegrees = 3.0 // Rotation angle for thumbnail text (degrees, clockwise)

	// Video overlay
	FramingLineHeight = 4 // Height in pixels of framing lines above/below center gap
)

// RuntimeConfig holds optional runtime overrides for customization
// When fields are nil/empty, the defaults from constants above are used
type RuntimeConfig struct {
	// Optional color overrides (RGB values 0-255)
	BarColorR *uint8
	BarColorG *uint8
	BarColorB *uint8

	TextColorR *uint8
	TextColorG *uint8
	TextColorB *uint8

	// Optional image path overrides
	BackgroundImagePath string
	ThumbnailImagePath  string
}

// GetBarColor returns the bar color RGB values (uses override or default)
func (c *RuntimeConfig) GetBarColor() (r, g, b uint8) {
	if c.BarColorR != nil && c.BarColorG != nil && c.BarColorB != nil {
		return *c.BarColorR, *c.BarColorG, *c.BarColorB
	}
	return BarColorR, BarColorG, BarColorB
}

// GetTextColor returns the text color RGB values (uses override or default)
func (c *RuntimeConfig) GetTextColor() (r, g, b uint8) {
	if c.TextColorR != nil && c.TextColorG != nil && c.TextColorB != nil {
		return *c.TextColorR, *c.TextColorG, *c.TextColorB
	}
	return TextColorR, TextColorG, TextColorB
}

// GetBackgroundImagePath returns the background image path (uses override or default embedded asset)
func (c *RuntimeConfig) GetBackgroundImagePath() string {
	if c.BackgroundImagePath != "" {
		return c.BackgroundImagePath
	}
	return BackgroundImageAsset
}

// GetThumbnailImagePath returns the thumbnail image path (uses override or default embedded asset)
func (c *RuntimeConfig) GetThumbnailImagePath() string {
	if c.ThumbnailImagePath != "" {
		return c.ThumbnailImagePath
	}
	return ThumbnailImageAsset
}

// ParseHexColor parses a hex color string (#RRGGBB or RRGGBB) and returns RGB values
func ParseHexColor(hex string) (r, g, b uint8, err error) {
	// Remove leading # if present
	hex = strings.TrimPrefix(hex, "#")

	// Validate length
	if len(hex) != 6 {
		return 0, 0, 0, fmt.Errorf("invalid hex color format: must be 6 characters (RRGGBB)")
	}

	// Parse RGB components
	var rgb uint64
	rgb, err = strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid hex color: %w", err)
	}

	r = uint8((rgb >> 16) & 0xFF)
	g = uint8((rgb >> 8) & 0xFF)
	b = uint8(rgb & 0xFF)

	return r, g, b, nil
}
