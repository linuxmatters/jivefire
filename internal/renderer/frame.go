package renderer

import (
	"image"
	"io"

	"github.com/linuxmatters/visualizer-go/internal/config"
	"golang.org/x/image/font"
)

// Frame represents a single video frame with visualization bars
type Frame struct {
	img        *image.RGBA
	bgImage    *image.RGBA
	fontFace   font.Face
	centerY    int
	startX     int
	totalWidth int
}

// NewFrame creates a new frame renderer
func NewFrame(bgImage *image.RGBA, fontFace font.Face) *Frame {
	// Calculate starting position to center all bars
	totalWidth := config.NumBars*config.BarWidth + (config.NumBars-1)*config.BarGap
	startX := (config.Width - totalWidth) / 2
	centerY := config.Height / 2

	return &Frame{
		img:        image.NewRGBA(image.Rect(0, 0, config.Width, config.Height)),
		bgImage:    bgImage,
		fontFace:   fontFace,
		centerY:    centerY,
		startX:     startX,
		totalWidth: totalWidth,
	}
}

// Draw renders the visualization bars and text onto the frame
func (f *Frame) Draw(barHeights []float64) {
	// Clear or copy background
	if f.bgImage != nil {
		copy(f.img.Pix, f.bgImage.Pix)
	} else {
		// Fast clear to black - memset style
		for i := 0; i < len(f.img.Pix); i += 4 {
			f.img.Pix[i] = 0     // R
			f.img.Pix[i+1] = 0   // G
			f.img.Pix[i+2] = 0   // B
			f.img.Pix[i+3] = 255 // A
		}
	}

	// Draw bars
	for i, h := range barHeights {
		barHeight := int(h)
		x := f.startX + i*(config.BarWidth+config.BarGap)
		if x+config.BarWidth > config.Width {
			continue
		}

		// Draw bar upward from center (with gap/2 offset) with subtle alpha gradient
		for y := f.centerY - barHeight - config.CenterGap/2; y < f.centerY-config.CenterGap/2; y++ {
			if y >= 0 && y < config.Height {
				// Calculate distance from center (0.0 at center, 1.0 at tip)
				distanceFromCenter := float64(f.centerY-config.CenterGap/2-y) / float64(barHeight)
				// Gradient: 1.0 (full) at center to 0.5 (50%) at tip
				alphaFactor := 1.0 - (distanceFromCenter * 0.5)

				offset := y*f.img.Stride + x*4
				for px := 0; px < config.BarWidth; px++ {
					pixOffset := offset + px*4
					// Get background color
					bgR := f.img.Pix[pixOffset]
					bgG := f.img.Pix[pixOffset+1]
					bgB := f.img.Pix[pixOffset+2]

					// Alpha blend: result = bar*alpha + bg*(1-alpha)
					f.img.Pix[pixOffset] = uint8(float64(config.BarColorR)*alphaFactor + float64(bgR)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+1] = uint8(float64(config.BarColorG)*alphaFactor + float64(bgG)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+2] = uint8(float64(config.BarColorB)*alphaFactor + float64(bgB)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+3] = 255
				}
			}
		}

		// Draw mirror bar downward from center (with gap/2 offset) with subtle alpha gradient
		for y := f.centerY + config.CenterGap/2; y < f.centerY+barHeight+config.CenterGap/2; y++ {
			if y >= 0 && y < config.Height {
				// Calculate distance from center (0.0 at center, 1.0 at tip)
				distanceFromCenter := float64(y-(f.centerY+config.CenterGap/2)) / float64(barHeight)
				// Gradient: 1.0 (full) at center to 0.5 (50%) at tip
				alphaFactor := 1.0 - (distanceFromCenter * 0.5)

				offset := y*f.img.Stride + x*4
				for px := 0; px < config.BarWidth; px++ {
					pixOffset := offset + px*4
					// Get background color
					bgR := f.img.Pix[pixOffset]
					bgG := f.img.Pix[pixOffset+1]
					bgB := f.img.Pix[pixOffset+2]

					// Alpha blend: result = bar*alpha + bg*(1-alpha)
					f.img.Pix[pixOffset] = uint8(float64(config.BarColorR)*alphaFactor + float64(bgR)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+1] = uint8(float64(config.BarColorG)*alphaFactor + float64(bgG)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+2] = uint8(float64(config.BarColorB)*alphaFactor + float64(bgB)*(1.0-alphaFactor))
					f.img.Pix[pixOffset+3] = 255
				}
			}
		}
	}

	// Draw center text and episode number on top of bars
	if f.fontFace != nil {
		DrawCenterText(f.img, f.fontFace, "Linux Matters Sample Text", f.centerY)
		DrawEpisodeNumber(f.img, f.fontFace, "00")
	}
}

// GetImage returns the current frame image
func (f *Frame) GetImage() *image.RGBA {
	return f.img
}

// WriteRawRGB writes raw RGB24 data to a writer (typically FFmpeg stdin)
func WriteRawRGB(w io.Writer, img *image.RGBA) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Pre-allocate buffer for one row (3 bytes per pixel for RGB24)
	rowBuf := make([]byte, width*3)

	for y := 0; y < height; y++ {
		// Direct access to RGBA pixel buffer (4 bytes per pixel)
		rowStart := y * img.Stride
		bufIdx := 0

		for x := 0; x < width; x++ {
			pixelIdx := rowStart + x*4
			rowBuf[bufIdx] = img.Pix[pixelIdx]     // R
			rowBuf[bufIdx+1] = img.Pix[pixelIdx+1] // G
			rowBuf[bufIdx+2] = img.Pix[pixelIdx+2] // B
			bufIdx += 3
		}

		w.Write(rowBuf)
	}
}
