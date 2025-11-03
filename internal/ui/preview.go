package ui

import (
	"image"
	"image/color"
)

// PreviewConfig holds configuration for the video preview
type PreviewConfig struct {
	Width  int // Width in terminal cells
	Height int // Height in terminal cells
}

// DefaultPreviewConfig returns a sensible default preview size
// Using 80x24 to fit nicely in the UI while maintaining 16:9-ish aspect ratio
func DefaultPreviewConfig() PreviewConfig {
	return PreviewConfig{
		Width:  80,
		Height: 24,
	}
}

// DownsampleFrame takes a full-resolution RGB frame and downsamples it to preview size
// Each terminal cell represents a rectangular region of the source image
func DownsampleFrame(frame *image.RGBA, config PreviewConfig) [][]color.Gray {
	bounds := frame.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Calculate how many source pixels each terminal cell represents
	cellWidth := srcWidth / config.Width
	cellHeight := srcHeight / config.Height

	preview := make([][]color.Gray, config.Height)
	for row := 0; row < config.Height; row++ {
		preview[row] = make([]color.Gray, config.Width)
		for col := 0; col < config.Width; col++ {
			// Calculate the region of the source image this cell represents
			srcX := col * cellWidth
			srcY := row * cellHeight

			// Sample the center pixel of this region
			// (Could average multiple pixels for better quality, but this is fast)
			r, g, b, _ := frame.At(srcX, srcY).RGBA()

			// Convert to 8-bit and calculate luminance
			gray := rgbToGrayscale(uint8(r>>8), uint8(g>>8), uint8(b>>8))
			preview[row][col] = color.Gray{Y: gray}
		}
	}

	return preview
}

// rgbToGrayscale converts RGB to grayscale using standard luminance formula
// Y = 0.299*R + 0.587*G + 0.114*B
func rgbToGrayscale(r, g, b uint8) uint8 {
	// Use integer arithmetic for speed (coefficients scaled by 256)
	// 0.299 ≈ 77/256, 0.587 ≈ 150/256, 0.114 ≈ 29/256
	return uint8((77*uint32(r) + 150*uint32(g) + 29*uint32(b)) >> 8)
}

// RenderPreview converts a grayscale preview grid to a string representation
// using Unicode block characters for grayscale rendering
func RenderPreview(preview [][]color.Gray) string {
	if len(preview) == 0 {
		return ""
	}

	// Build the preview string using Unicode block characters
	// We'll use background colors to represent grayscale values
	var result string

	// Top border
	result += "  Video Preview:\n"
	result += "  ┌" + repeat("─", len(preview[0])) + "┐\n"

	// Render each row
	for _, row := range preview {
		result += "  │"
		for _, pixel := range row {
			// Map 0-255 grayscale to a shade character
			// Using a gradient of block characters for better visual quality
			char := grayscaleToChar(pixel.Y)
			result += char
		}
		result += "│\n"
	}

	// Bottom border
	result += "  └" + repeat("─", len(preview[0])) + "┘\n"

	return result
}

// grayscaleToChar maps a grayscale value (0-255) to a Unicode block character
// Creates a smooth gradient from dark to light
func grayscaleToChar(gray uint8) string {
	// 10 levels of brightness using Unicode block elements
	blocks := []string{" ", "░", "▒", "▓", "█", "█", "█", "█", "█", "█"}
	index := int(gray) * len(blocks) / 256
	if index >= len(blocks) {
		index = len(blocks) - 1
	}
	return blocks[index]
}

// repeat creates a string by repeating s n times
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
