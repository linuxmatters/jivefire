package ui

import (
	"fmt"
	"image"
	"image/color"
)

// PreviewConfig holds configuration for the video preview
type PreviewConfig struct {
	Width  int // Width in terminal cells
	Height int // Height in terminal cells
}

// DefaultPreviewConfig returns a sensible default preview size
// Using 72x20 1.8:1 (slightly wider than 16:9 but very close)
func DefaultPreviewConfig() PreviewConfig {
	return PreviewConfig{
		Width:  72,
		Height: 20,
	}
}

// DownsampleFrame takes a full-resolution RGB frame and downsamples it to preview size
// Each terminal cell represents a rectangular region of the source image
// Averages all pixels in each region for smooth, high-quality downsampling
func DownsampleFrame(frame *image.RGBA, config PreviewConfig) [][]color.RGBA {
	bounds := frame.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Calculate how many source pixels each terminal cell represents
	cellWidth := srcWidth / config.Width
	cellHeight := srcHeight / config.Height

	preview := make([][]color.RGBA, config.Height)
	for row := 0; row < config.Height; row++ {
		preview[row] = make([]color.RGBA, config.Width)
		for col := 0; col < config.Width; col++ {
			// Calculate the region of the source image this cell represents
			srcX := col * cellWidth
			srcY := row * cellHeight

			// Average all pixels in this cell region for better quality
			var sumR, sumG, sumB uint32
			pixelCount := 0

			for y := srcY; y < srcY+cellHeight && y < srcHeight; y++ {
				for x := srcX; x < srcX+cellWidth && x < srcWidth; x++ {
					r, g, b, _ := frame.At(x, y).RGBA()
					// RGBA() returns 16-bit values, convert to 8-bit
					sumR += uint32(r >> 8)
					sumG += uint32(g >> 8)
					sumB += uint32(b >> 8)
					pixelCount++
				}
			}

			// Calculate average RGB values
			if pixelCount > 0 {
				avgR := uint8(sumR / uint32(pixelCount))
				avgG := uint8(sumG / uint32(pixelCount))
				avgB := uint8(sumB / uint32(pixelCount))

				preview[row][col] = color.RGBA{R: avgR, G: avgG, B: avgB, A: 255}
			}
		}
	}

	return preview
}

// RenderPreview converts an RGB preview grid to a string representation
// using ANSI 24-bit true color escape codes for beautiful colored rendering
func RenderPreview(preview [][]color.RGBA) string {
	if len(preview) == 0 {
		return ""
	}

	// Build the preview string using ANSI 24-bit RGB background colors
	// Format: \x1b[48;2;R;G;Bm for background color, space character as pixel, \x1b[0m to reset
	var result string

	// Top border
	result += "  Video Preview:\n"
	result += "  ┌" + repeat("─", len(preview[0])) + "┐\n"

	// Render each row with true color
	for _, row := range preview {
		result += "  │"
		for _, pixel := range row {
			// ANSI escape: \x1b[48;2;R;G;Bm sets 24-bit RGB background color
			result += fmt.Sprintf("\x1b[48;2;%d;%d;%dm \x1b[0m", pixel.R, pixel.G, pixel.B)
		}
		result += "│\n"
	}

	// Bottom border
	result += "  └" + repeat("─", len(preview[0])) + "┘\n"

	return result
}

// repeat creates a string by repeating s n times
func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
