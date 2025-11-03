package renderer

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/draw"
)

// Create a test image for benchmarking
func createTestImage(width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Create a gradient pattern
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := uint8(float64(x) / float64(width) * 255)
			g := uint8(float64(y) / float64(height) * 255)
			b := uint8((float64(x+y) / float64(width+height)) * 255)
			img.SetRGBA(x, y, color.RGBA{r, g, b, 255})
		}
	}

	return img
}

// Save test image to file
func saveTestImage(t *testing.T) string {
	// Create a large test image (typical photo size)
	img := createTestImage(3840, 2160) // 4K resolution

	// Save to temp file
	f, err := os.CreateTemp("", "test-image-*.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	err = png.Encode(f, img)
	if err != nil {
		t.Fatal(err)
	}

	return f.Name()
}

func BenchmarkOriginalBilinear(b *testing.B) {
	// Set test dimensions
	config.Width = 1920
	config.Height = 1080

	filename := saveTestImage(&testing.T{})
	defer os.Remove(filename)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadBackgroundImage(filename)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOptimizedBilinear(b *testing.B) {
	// Set test dimensions
	config.Width = 1920
	config.Height = 1080

	filename := saveTestImage(&testing.T{})
	defer os.Remove(filename)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := LoadBackgroundImageOptimized(filename)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmark just the scaling operation (without file I/O)
func BenchmarkScalingOnly(b *testing.B) {
	// Create source image once
	src := createTestImage(3840, 2160)

	b.Run("Original", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))

			scaleX := float64(3840) / float64(1920)
			scaleY := float64(2160) / float64(1080)

			for y := 0; y < 1080; y++ {
				for x := 0; x < 1920; x++ {
					// Bilinear interpolation
					srcX := float64(x) * scaleX
					srcY := float64(y) * scaleY

					x0 := int(srcX)
					y0 := int(srcY)
					x1 := x0 + 1
					y1 := y0 + 1

					if x1 >= 3840 {
						x1 = 3839
					}
					if y1 >= 2160 {
						y1 = 2159
					}

					c00 := src.At(x0, y0)
					c10 := src.At(x1, y0)
					c01 := src.At(x0, y1)
					c11 := src.At(x1, y1)

					fx := srcX - float64(x0)
					fy := srcY - float64(y0)

					r00, g00, b00, a00 := c00.RGBA()
					r10, g10, b10, a10 := c10.RGBA()
					r01, g01, b01, a01 := c01.RGBA()
					r11, g11, b11, a11 := c11.RGBA()

					r := uint8((float64(r00>>8)*(1-fx)*(1-fy) +
						float64(r10>>8)*fx*(1-fy) +
						float64(r01>>8)*(1-fx)*fy +
						float64(r11>>8)*fx*fy))

					g := uint8((float64(g00>>8)*(1-fx)*(1-fy) +
						float64(g10>>8)*fx*(1-fy) +
						float64(g01>>8)*(1-fx)*fy +
						float64(g11>>8)*fx*fy))

					b_val := uint8((float64(b00>>8)*(1-fx)*(1-fy) +
						float64(b10>>8)*fx*(1-fy) +
						float64(b01>>8)*(1-fx)*fy +
						float64(b11>>8)*fx*fy))

					a := uint8((float64(a00>>8)*(1-fx)*(1-fy) +
						float64(a10>>8)*fx*(1-fy) +
						float64(a01>>8)*(1-fx)*fy +
						float64(a11>>8)*fx*fy))

					dst.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b_val, A: a})
				}
			}
		}
	})

	b.Run("X/Image/Draw", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			dst := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
			draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
		}
	})
}
