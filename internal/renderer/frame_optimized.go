package renderer

import (
	"image"
	"sync"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/font"
)

// OptimizedFrame represents a single video frame with visualization bars
type OptimizedFrame struct {
	img        *image.RGBA
	bgImage    *image.RGBA
	fontFace   font.Face
	centerY    int
	startX     int
	totalWidth int

	// Pre-computed values
	maxBarHeight  int
	alphaTable    []uint8     // Pre-computed alpha values for gradient
	barColorTable [][3]uint8  // Pre-computed bar colors at different alpha levels
	textOverlay   *image.RGBA // Pre-rendered text overlay
	hasBackground bool
}

var framePool = sync.Pool{
	New: func() interface{} {
		return image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	},
}

// NewOptimizedFrame creates a new optimized frame renderer
func NewOptimizedFrame(bgImage *image.RGBA, fontFace font.Face) *OptimizedFrame {
	totalWidth := config.NumBars*config.BarWidth + (config.NumBars-1)*config.BarGap
	startX := (config.Width - totalWidth) / 2
	centerY := config.Height / 2

	// Calculate maximum possible bar height
	maxBarHeight := centerY - config.CenterGap/2

	// Pre-compute alpha gradient table (0.5 to 1.0 range)
	alphaTable := make([]uint8, maxBarHeight)
	for i := 0; i < maxBarHeight; i++ {
		distanceFromCenter := float64(i) / float64(maxBarHeight)
		alphaFactor := 1.0 - (distanceFromCenter * 0.5)
		alphaTable[i] = uint8(alphaFactor * 255)
	}

	// Pre-compute bar colors at different alpha levels (0-255)
	barColorTable := make([][3]uint8, 256)
	for alpha := 0; alpha < 256; alpha++ {
		factor := float64(alpha) / 255.0
		barColorTable[alpha][0] = uint8(float64(config.BarColorR) * factor)
		barColorTable[alpha][1] = uint8(float64(config.BarColorG) * factor)
		barColorTable[alpha][2] = uint8(float64(config.BarColorB) * factor)
	}

	f := &OptimizedFrame{
		img:           framePool.Get().(*image.RGBA),
		bgImage:       bgImage,
		fontFace:      fontFace,
		centerY:       centerY,
		startX:        startX,
		totalWidth:    totalWidth,
		maxBarHeight:  maxBarHeight,
		alphaTable:    alphaTable,
		barColorTable: barColorTable,
		hasBackground: bgImage != nil,
	}

	// Text overlay rendering will be done directly for now
	// TODO: Implement efficient pre-rendered text overlay

	return f
}

// DrawOptimized renders the visualization bars using pre-computed values
func (f *OptimizedFrame) DrawOptimized(barHeights []float64) {
	// Clear or copy background
	if f.hasBackground {
		copy(f.img.Pix, f.bgImage.Pix)
	} else {
		// Fast clear to black using optimized pattern
		// Clear 8 pixels at a time for better memory bandwidth
		blackPattern := [32]byte{
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
		}
		for i := 0; i < len(f.img.Pix); i += 32 {
			copy(f.img.Pix[i:i+32], blackPattern[:])
		}
	}

	// Draw bars with optimized algorithm
	if f.hasBackground {
		f.drawBarsWithBackground(barHeights)
	} else {
		f.drawBarsNoBackground(barHeights) // Much faster path for black background
	}

	// Apply pre-rendered text overlay
	if f.textOverlay != nil {
		f.applyTextOverlay()
	}
}

// drawBarsNoBackground optimized path when no background (just black)
func (f *OptimizedFrame) drawBarsNoBackground(barHeights []float64) {
	for i, h := range barHeights {
		barHeight := int(h)
		if barHeight <= 0 {
			continue
		}

		x := f.startX + i*(config.BarWidth+config.BarGap)
		if x+config.BarWidth > config.Width {
			continue
		}

		// Clamp bar height
		if barHeight > f.maxBarHeight {
			barHeight = f.maxBarHeight
		}

		// Draw upward bar
		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		for y := yStart; y < yEnd; y++ {
			if y < 0 {
				continue
			}

			// Get pre-computed alpha for this height
			heightFromBottom := y - yStart
			alpha := f.alphaTable[barHeight-1-heightFromBottom]
			colors := &f.barColorTable[alpha]

			// Write entire bar width at once
			offset := y*f.img.Stride + x*4
			for px := 0; px < config.BarWidth; px++ {
				pixOffset := offset + px*4
				f.img.Pix[pixOffset] = colors[0]
				f.img.Pix[pixOffset+1] = colors[1]
				f.img.Pix[pixOffset+2] = colors[2]
				f.img.Pix[pixOffset+3] = 255
			}
		}

		// Draw downward bar (mirror)
		yStart = f.centerY + config.CenterGap/2
		yEnd = f.centerY + barHeight + config.CenterGap/2

		for y := yStart; y < yEnd; y++ {
			if y >= config.Height {
				break
			}

			// Get pre-computed alpha for this height
			heightFromTop := y - yStart
			alpha := f.alphaTable[heightFromTop]
			colors := &f.barColorTable[alpha]

			// Write entire bar width at once
			offset := y*f.img.Stride + x*4
			for px := 0; px < config.BarWidth; px++ {
				pixOffset := offset + px*4
				f.img.Pix[pixOffset] = colors[0]
				f.img.Pix[pixOffset+1] = colors[1]
				f.img.Pix[pixOffset+2] = colors[2]
				f.img.Pix[pixOffset+3] = 255
			}
		}
	}
}

// drawBarsWithBackground handles alpha blending with background
func (f *OptimizedFrame) drawBarsWithBackground(barHeights []float64) {
	// This path is only used when there's an actual background image
	// For now, keeping similar to original but with pre-computed tables

	for i, h := range barHeights {
		barHeight := int(h)
		if barHeight <= 0 {
			continue
		}

		x := f.startX + i*(config.BarWidth+config.BarGap)
		if x+config.BarWidth > config.Width {
			continue
		}

		if barHeight > f.maxBarHeight {
			barHeight = f.maxBarHeight
		}

		// Draw upward bar
		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		for y := yStart; y < yEnd; y++ {
			if y < 0 {
				continue
			}

			heightFromBottom := y - yStart
			alpha := f.alphaTable[barHeight-1-heightFromBottom]
			alphaF := float64(alpha) / 255.0
			invAlphaF := 1.0 - alphaF

			offset := y*f.img.Stride + x*4
			for px := 0; px < config.BarWidth; px++ {
				pixOffset := offset + px*4

				// Alpha blend with background
				bgR := f.img.Pix[pixOffset]
				bgG := f.img.Pix[pixOffset+1]
				bgB := f.img.Pix[pixOffset+2]

				f.img.Pix[pixOffset] = uint8(float64(config.BarColorR)*alphaF + float64(bgR)*invAlphaF)
				f.img.Pix[pixOffset+1] = uint8(float64(config.BarColorG)*alphaF + float64(bgG)*invAlphaF)
				f.img.Pix[pixOffset+2] = uint8(float64(config.BarColorB)*alphaF + float64(bgB)*invAlphaF)
			}
		}

		// Similar for downward bar...
		yStart = f.centerY + config.CenterGap/2
		yEnd = f.centerY + barHeight + config.CenterGap/2

		for y := yStart; y < yEnd; y++ {
			if y >= config.Height {
				break
			}

			heightFromTop := y - yStart
			alpha := f.alphaTable[heightFromTop]
			alphaF := float64(alpha) / 255.0
			invAlphaF := 1.0 - alphaF

			offset := y*f.img.Stride + x*4
			for px := 0; px < config.BarWidth; px++ {
				pixOffset := offset + px*4

				bgR := f.img.Pix[pixOffset]
				bgG := f.img.Pix[pixOffset+1]
				bgB := f.img.Pix[pixOffset+2]

				f.img.Pix[pixOffset] = uint8(float64(config.BarColorR)*alphaF + float64(bgR)*invAlphaF)
				f.img.Pix[pixOffset+1] = uint8(float64(config.BarColorG)*alphaF + float64(bgG)*invAlphaF)
				f.img.Pix[pixOffset+2] = uint8(float64(config.BarColorB)*alphaF + float64(bgB)*invAlphaF)
			}
		}
	}
}

// applyTextOverlay blends pre-rendered text onto the frame
func (f *OptimizedFrame) applyTextOverlay() {
	// For now, just call the original text drawing functions
	// TODO: Optimize text overlay blending
	if f.fontFace != nil {
		DrawCenterText(f.img, f.fontFace, "Linux Matters Sample Text", f.centerY)
		DrawEpisodeNumber(f.img, f.fontFace, "00")
	}
}

// GetImage returns the current frame image
func (f *OptimizedFrame) GetImage() *image.RGBA {
	return f.img
}

// Release returns the frame buffer to the pool
func (f *OptimizedFrame) Release() {
	if f.img != nil {
		framePool.Put(f.img)
		f.img = nil
	}
}
