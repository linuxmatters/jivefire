package renderer

import (
	"fmt"
	"image"
	"sync"

	"github.com/linuxmatters/jivefire/internal/config"
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

	// Text overlay
	episodeNum string
	title      string

	// Pre-computed values
	maxBarHeight  int
	alphaTable    []uint8    // Pre-computed alpha values for gradient
	barColorTable [][3]uint8 // Pre-computed bar colors at different alpha levels
	hasBackground bool
}

var framePool = sync.Pool{
	New: func() interface{} {
		return image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	},
}

// NewFrame creates a new optimized frame renderer
func NewFrame(bgImage *image.RGBA, fontFace font.Face, episodeNum int, title string) *Frame {
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

	// Format episode number as two-digit string
	episodeStr := "00"
	if episodeNum > 0 {
		episodeStr = formatEpisodeNumber(episodeNum)
	}

	f := &Frame{
		img:           framePool.Get().(*image.RGBA),
		bgImage:       bgImage,
		fontFace:      fontFace,
		centerY:       centerY,
		startX:        startX,
		totalWidth:    totalWidth,
		episodeNum:    episodeStr,
		title:         title,
		maxBarHeight:  maxBarHeight,
		alphaTable:    alphaTable,
		barColorTable: barColorTable,
		hasBackground: bgImage != nil,
	}

	return f
}

// Draw renders the visualization bars using pre-computed values
func (f *Frame) Draw(barHeights []float64) {
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

	// Draw bars with vertical symmetry optimization
	f.drawBars(barHeights)

	// Apply text overlay
	if f.fontFace != nil {
		f.applyTextOverlay()
	}
}

// drawBars renders all bars using horizontal + vertical symmetry optimization.
// The frequency data is arranged symmetrically: bars 0-31 are mirrored to create bars 32-63.
// We render only the first 32 bars upward, then mirror 3 times:
//  1. Vertical mirror → bars 0-31 downward
//  2. Horizontal mirror → bars 32-63 upward
//  3. Both mirrors → bars 32-63 downward
//
// This renders 1/4 of the pixels and is ~4x faster.
func (f *Frame) drawBars(barHeights []float64) {
	// Pre-allocate pixel pattern buffer (reused for all bars)
	pixelPattern := make([]byte, config.BarWidth*4)

	// Render only left half (bars 0-31), upward only
	halfBars := config.NumBars / 2
	for i := 0; i < halfBars; i++ {
		barHeight := int(barHeights[i])
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

		// Render upward bar only (left half)
		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		if f.hasBackground {
			f.renderBarWithBackground(x, yStart, yEnd, barHeight)
		} else {
			f.renderBarNoBackground(x, yStart, yEnd, barHeight, pixelPattern)
		}
	}

	// Now mirror in 3 operations to fill remaining 3/4 of the bars:
	// 1. Vertical mirror: bars 0-31 upward → bars 0-31 downward
	// 2. Horizontal mirror: bars 0-31 upward → bars 32-63 upward
	// 3. Both mirrors: bars 0-31 upward → bars 32-63 downward
	for i := 0; i < halfBars; i++ {
		barHeight := int(barHeights[i])
		if barHeight <= 0 {
			continue
		}

		xLeft := f.startX + i*(config.BarWidth+config.BarGap)
		xRight := f.startX + (config.NumBars-1-i)*(config.BarWidth+config.BarGap)

		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		// 1. Vertical mirror: create left-side downward bars
		f.mirrorBarVertical(xLeft, yStart, yEnd)

		// 2. Horizontal mirror: create right-side upward bars
		f.mirrorBarHorizontal(xLeft, xRight, yStart, yEnd)

		// 3. Both mirrors: create right-side downward bars
		f.mirrorBarVertical(xRight, yStart, yEnd)
	}
}

// renderBarNoBackground renders a single upward bar without background blending
func (f *Frame) renderBarNoBackground(x, yStart, yEnd, barHeight int, pixelPattern []byte) {
	for y := yStart; y < yEnd; y++ {
		if y < 0 {
			continue
		}

		// Calculate alpha for fade gradient (dim at tip → bright at center)
		distanceFromCenter := yEnd - 1 - y
		alphaIndex := (distanceFromCenter * f.maxBarHeight) / barHeight
		if alphaIndex >= f.maxBarHeight {
			alphaIndex = f.maxBarHeight - 1
		}
		alpha := f.alphaTable[alphaIndex]
		colors := &f.barColorTable[alpha]

		// Fill pixel pattern once for this scanline
		for px := 0; px < config.BarWidth; px++ {
			offset := px * 4
			pixelPattern[offset] = colors[0]
			pixelPattern[offset+1] = colors[1]
			pixelPattern[offset+2] = colors[2]
			pixelPattern[offset+3] = 255
		}

		// Write entire bar width with single copy
		offset := y*f.img.Stride + x*4
		copy(f.img.Pix[offset:offset+config.BarWidth*4], pixelPattern)
	}
}

// renderBarWithBackground renders a single upward bar with background blending
func (f *Frame) renderBarWithBackground(x, yStart, yEnd, barHeight int) {
	for y := yStart; y < yEnd; y++ {
		if y < 0 {
			continue
		}

		// Calculate alpha for fade gradient (dim at tip → bright at center)
		distanceFromCenter := yEnd - 1 - y
		alphaIndex := (distanceFromCenter * f.maxBarHeight) / barHeight
		if alphaIndex >= f.maxBarHeight {
			alphaIndex = f.maxBarHeight - 1
		}
		alpha := f.alphaTable[alphaIndex]
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
}

// mirrorBarVertical creates downward bar by mirroring upward bar pixels.
// Copies scanlines in reverse order to preserve the fade gradient.
func (f *Frame) mirrorBarVertical(x, yStart, yEnd int) {
	upwardHeight := yEnd - yStart
	downStart := f.centerY + config.CenterGap/2

	// Copy each scanline from upward bar in reverse order
	for i := 0; i < upwardHeight; i++ {
		srcY := yEnd - 1 - i  // Read from bottom of upward bar
		dstY := downStart + i // Write to top of downward bar

		if srcY < 0 || dstY >= config.Height {
			continue
		}

		srcOffset := srcY*f.img.Stride + x*4
		dstOffset := dstY*f.img.Stride + x*4
		copy(f.img.Pix[dstOffset:dstOffset+config.BarWidth*4],
			f.img.Pix[srcOffset:srcOffset+config.BarWidth*4])
	}
}

// mirrorBarHorizontal creates right-side bar by copying left-side bar pixels.
// Copies the entire upward bar from left position to right position.
func (f *Frame) mirrorBarHorizontal(xLeft, xRight, yStart, yEnd int) {
	// Copy each scanline from left bar to right bar
	for y := yStart; y < yEnd; y++ {
		if y < 0 || y >= config.Height {
			continue
		}

		srcOffset := y*f.img.Stride + xLeft*4
		dstOffset := y*f.img.Stride + xRight*4
		copy(f.img.Pix[dstOffset:dstOffset+config.BarWidth*4],
			f.img.Pix[srcOffset:srcOffset+config.BarWidth*4])
	}
}

// applyTextOverlay renders text onto the frame
func (f *Frame) applyTextOverlay() {
	if f.fontFace != nil {
		DrawCenterText(f.img, f.fontFace, f.title, f.centerY)
		DrawEpisodeNumber(f.img, f.fontFace, f.episodeNum)
	}
}

// formatEpisodeNumber formats an episode number as a two-digit string
func formatEpisodeNumber(num int) string {
	if num < 100 {
		return fmt.Sprintf("%02d", num)
	}
	// For numbers >= 100, return as-is
	return fmt.Sprintf("%d", num)
}

// GetImage returns the current frame image
func (f *Frame) GetImage() *image.RGBA {
	return f.img
}

// Release returns the frame buffer to the pool
func (f *Frame) Release() {
	if f.img != nil {
		framePool.Put(f.img)
		f.img = nil
	}
}
