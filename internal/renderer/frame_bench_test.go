package renderer

import (
	"image"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/font/basicfont"
)

// generateTestBarHeights creates test bar heights for benchmarking
func generateTestBarHeights() []float64 {
	heights := make([]float64, config.NumBars)
	for i := range heights {
		// Create a wave pattern
		heights[i] = float64(config.Height/4) * (1.0 + float64(i%8)/8.0)
	}
	return heights
}

// BenchmarkOriginalFrame benchmarks the original frame rendering
func BenchmarkOriginalFrame(b *testing.B) {
	// Setup
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	// No font - focus on bar drawing performance
	frame := NewFrame(bgImage, nil)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Draw(barHeights)
	}
}

// BenchmarkOptimizedFrame benchmarks the optimized frame rendering
func BenchmarkOptimizedFrame(b *testing.B) {
	// Setup
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	// No font - focus on bar drawing performance
	frame := NewOptimizedFrame(bgImage, nil)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.DrawOptimized(barHeights)
	}
}

// BenchmarkOptimizedFrameNoBackground benchmarks optimized rendering without background
func BenchmarkOptimizedFrameNoBackground(b *testing.B) {
	// Setup - no background
	// No font - focus on bar drawing performance
	frame := NewOptimizedFrame(nil, nil)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.DrawOptimized(barHeights)
	}
}

// BenchmarkOriginalFrameNoBackground benchmarks original rendering without background
func BenchmarkOriginalFrameNoBackground(b *testing.B) {
	// Setup - no background
	// No font - focus on bar drawing performance
	frame := NewFrame(nil, nil)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Draw(barHeights)
	}
}

// Comparison test to ensure output is visually identical
func TestFrameOutputComparison(t *testing.T) {
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	// Fill with a simple pattern
	for y := 0; y < config.Height; y++ {
		for x := 0; x < config.Width; x++ {
			offset := y*bgImage.Stride + x*4
			bgImage.Pix[offset] = uint8(x % 256)
			bgImage.Pix[offset+1] = uint8(y % 256)
			bgImage.Pix[offset+2] = uint8((x + y) % 256)
			bgImage.Pix[offset+3] = 255
		}
	}

	fontFace := basicfont.Face7x13
	originalFrame := NewFrame(bgImage, fontFace)
	optimizedFrame := NewOptimizedFrame(bgImage, fontFace)

	barHeights := generateTestBarHeights()

	originalFrame.Draw(barHeights)
	optimizedFrame.DrawOptimized(barHeights)

	// Compare key areas (not pixel-perfect due to potential floating point differences)
	// Just check a few representative pixels
	orig := originalFrame.GetImage()
	opt := optimizedFrame.GetImage()

	// Check center pixels
	centerX := config.Width / 2
	centerY := config.Height / 2

	for dy := -10; dy <= 10; dy++ {
		for dx := -10; dx <= 10; dx++ {
			x := centerX + dx
			y := centerY + dy
			offset := y*orig.Stride + x*4

			// Allow small differences due to floating point
			for c := 0; c < 3; c++ {
				diff := int(orig.Pix[offset+c]) - int(opt.Pix[offset+c])
				if diff < 0 {
					diff = -diff
				}
				if diff > 2 {
					t.Errorf("Pixel difference at (%d,%d) channel %d: orig=%d opt=%d",
						x, y, c, orig.Pix[offset+c], opt.Pix[offset+c])
				}
			}
		}
	}
}
