package renderer

import (
	"image"
	"image/png"
	"os"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/draw"
)

// LoadBackgroundImageOptimized loads and scales a PNG background image using optimized scaling
func LoadBackgroundImageOptimized(filename string) (*image.RGBA, error) {
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

	// Create destination image
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	// Use golang.org/x/image/draw for optimized scaling
	// ApproxBiLinear is the fastest bilinear implementation
	// For even better quality, you could use CatmullRom or BiLinear
	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		// ApproxBiLinear provides fast bilinear interpolation
		draw.ApproxBiLinear.Scale(rgba, rgba.Bounds(), img, bounds, draw.Src, nil)
	} else {
		// Direct copy if dimensions match
		draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	}

	return rgba, nil
}

// LoadBackgroundImageRez loads and scales using the rez library for comparison
// Uncomment and add "github.com/bamiaux/rez" to imports if you want to test this
/*
func LoadBackgroundImageRez(filename string) (*image.RGBA, error) {
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

	// Create destination image
	rgba := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))

	if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
		// Convert to appropriate format if needed
		var src image.Image = img

		// If the source is YCbCr (common for JPEG), rez can handle it directly
		// Otherwise, we need to convert or use the RGBA directly
		filter := rez.NewBilinearFilter()
		err = rez.Convert(rgba, src, filter)
		if err != nil {
			return nil, err
		}
	} else {
		// Direct copy if dimensions match
		draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	}

	return rgba, nil
}
*/

// Benchmark function to compare implementations
func BenchmarkImageScaling(filename string) {
	// This would be used to compare the performance of different implementations
	// You could add timing code here to measure each approach
}
