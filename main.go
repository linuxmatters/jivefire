package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"gonum.org/v1/gonum/dsp/fourier"
)

const (
	// Video settings
	width  = 1280
	height = 720
	fps    = 30

	// Audio settings
	sampleRate = 44100
	fftSize    = 2048

	// Visualization settings
	numBars      = 64   // Number of bars
	barWidth     = 12   // Width of each bar
	barGap       = 8    // Gap between bars
	centerGap    = 100  // Gap between top and bottom bar sections
	maxBarHeight = 0.50 // Maximum bar height as fraction of available space

	// Colors
	barColorR = 164
	barColorG = 0
	barColorB = 0
)

func main() {
	var snapshotMode bool
	var snapshotTime float64

	flag.BoolVar(&snapshotMode, "snapshot", false, "Generate a single PNG frame instead of video")
	flag.Float64Var(&snapshotTime, "at", 1.0, "Timestamp in seconds for snapshot (default: 1.0)")
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		fmt.Println("Usage: visualizer-go [--snapshot] [--at=1.0] <input.wav> <output>")
		fmt.Println("  output: .mp4 for video, .png for snapshot")
		os.Exit(1)
	}

	inputFile := args[0]
	outputFile := args[1]

	fmt.Printf("Reading audio: %s\n", inputFile)
	samples, err := readWAV(inputFile)
	if err != nil {
		fmt.Printf("Error reading WAV: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Loaded %d samples\n", len(samples))

	if snapshotMode {
		generateSnapshot(samples, outputFile, snapshotTime)
		return
	}

	fmt.Printf("Generating visualization...\n")

	// Start FFmpeg process with optimized settings
	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "rawvideo",
		"-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", width, height),
		"-framerate", fmt.Sprintf("%d", fps),
		"-i", "pipe:0",
		"-i", inputFile,
		"-c:v", "libx264",
		"-preset", "ultrafast", // Much faster encoding
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "192k",
		"-pix_fmt", "yuv420p",
		"-shortest",
		outputFile,
	)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Printf("Error creating pipe: %v\n", err)
		os.Exit(1)
	}

	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting FFmpeg: %v\n", err)
		os.Exit(1)
	}

	// Calculate frames
	samplesPerFrame := sampleRate / fps
	numFrames := len(samples) / samplesPerFrame

	fft := fourier.NewFFT(fftSize)

	// Profiling variables
	var totalFFT, totalBin, totalDraw, totalWrite time.Duration
	startTime := time.Now()

	// Load background image
	bgImage, err := loadBackgroundImage("bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load bg.png: %v\n", err)
		fmt.Printf("Continuing with black background...\n")
		bgImage = nil
	}

	// Load font for center text
	fontFace, err := loadFont("Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fmt.Printf("Continuing without text...\n")
		fontFace = nil
	}

	// Reuse image buffer across frames to reduce allocations
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// CAVA algorithm implementation
	// Smoothing: track previous bar heights for temporal smoothing
	prevBarHeights := make([]float64, numBars)
	cavaPeaks := make([]float64, numBars)
	cavaFall := make([]float64, numBars)
	cavaMem := make([]float64, numBars)

	// CAVA defaults (from karlstav/cava source)
	const framerate = 30.0
	const noiseReduction = 0.77 // CAVA default integral smoothing
	const fallAccel = 0.028     // CAVA gravity acceleration constant

	// Calculate gravity modifier (CAVA formula)
	gravityMod := math.Pow(60.0/framerate, 2.5) * 1.54 / noiseReduction
	if gravityMod < 1.0 {
		gravityMod = 1.0
	}

	// Auto-sensitivity adjustment (CAVA-style)
	// Track running average of peak values to prevent constant topping out
	var sensitivity = 1.0

	for frame := 0; frame < numFrames; frame++ {
		start := frame * samplesPerFrame
		end := start + fftSize
		if end > len(samples) {
			end = len(samples)
		}

		// Get FFT of this chunk
		chunk := samples[start:end]
		// Pad if needed
		if len(chunk) < fftSize {
			padded := make([]float64, fftSize)
			copy(padded, chunk)
			chunk = padded
		}

		// Apply Hanning window
		windowed := applyHanning(chunk)

		// Compute FFT
		t0 := time.Now()
		coeffs := fft.Coefficients(nil, windowed)
		totalFFT += time.Since(t0)

		// Compute magnitudes and bin into bars (returns normalized 0.0-1.0 values)
		t0 = time.Now()
		barHeights := binFFT(coeffs, sensitivity)
		totalBin += time.Since(t0)

		// CAVA-style auto-sensitivity: work in normalized 0.0-1.0 space
		// CAVA uses 1.0 as the hard limit - any value > 1.0 is considered overshoot
		const overshootThreshold = 1.0 // CAVA clips at exactly 1.0

		overshootDetected := false
		for i, h := range barHeights {
			if h > overshootThreshold {
				overshootDetected = true
				// Clip to 1.0 immediately like CAVA does
				barHeights[i] = 1.0
			}
		}

		// CAVA-style sensitivity adjustment: asymmetric rates
		if overshootDetected {
			// Reduce sensitivity aggressively when overshooting
			sensitivity *= 0.90 // 10% reduction per frame (much more aggressive than CAVA's 1.5%)
		} else {
			// Increase sensitivity VERY slowly when undershooting (CAVA uses 1.002 = 0.2% increase)
			sensitivity *= 1.001 // 0.1% increase per frame - slow climb back up
		}

		// Clamp sensitivity to reasonable range
		if sensitivity < 0.05 {
			sensitivity = 0.05
		}
		if sensitivity > 2.0 {
			sensitivity = 2.0
		}

		// NOW apply maxBarHeight scaling to convert from normalized to pixel space
		// Available space is from bar start (centerY ± centerGap/2) to edge (0 or height)
		// This is: height/2 - centerGap/2 for each half
		actualAvailableSpace := float64(height/2 - centerGap/2)
		availableHeight := actualAvailableSpace * maxBarHeight
		for i := range barHeights {
			barHeights[i] *= availableHeight
			// Hard clip at available height
			if barHeights[i] > availableHeight {
				barHeights[i] = availableHeight
			}
		}

		// Apply CAVA-style gravity smoothing: responsive to changes
		for i := range barHeights {
			currentHeight := barHeights[i]

			// CAVA gravity-based decay
			if currentHeight < prevBarHeights[i] {
				// Falling: apply gravity with quadratic acceleration
				currentHeight = cavaPeaks[i] * (1.0 - (cavaFall[i] * cavaFall[i] * gravityMod))
				cavaFall[i] += fallAccel

				// Floor at zero
				if currentHeight < 0 {
					currentHeight = 0
				}
			} else {
				// Rising: new peak
				cavaPeaks[i] = currentHeight
				cavaFall[i] = 0.0
			}

			// CAVA integral smoothing (noise reduction)
			currentHeight = cavaMem[i]*noiseReduction + currentHeight
			cavaMem[i] = currentHeight

			// Soft knee compression instead of hard clip for smooth animation
			// Apply progressive attenuation as bars approach maxBarHeight limit
			// This creates a natural "squash" effect instead of abrupt clipping
			if currentHeight > availableHeight {
				// Soft knee: gradually compress values above limit
				// Use exponential decay to smoothly approach the limit
				overshoot := currentHeight - availableHeight
				// Compress overshoot: the more it overshoots, the more we compress
				// Formula: limit + overshoot * exp(-overshoot/limit)
				// This asymptotically approaches the limit but never quite reaches it
				currentHeight = availableHeight + overshoot*math.Exp(-overshoot/availableHeight)
			}

			prevBarHeights[i] = currentHeight
		}

		// Rearrange frequencies for center-out symmetric distribution
		rearrangedHeights := rearrangeFrequenciesCenterOut(prevBarHeights)

		// Generate frame image
		t0 = time.Now()
		drawFrame(rearrangedHeights, img, bgImage, fontFace)
		totalDraw += time.Since(t0)

		// Write raw RGB to FFmpeg
		t0 = time.Now()
		writeRawRGB(stdin, img)
		totalWrite += time.Since(t0)

		if frame%30 == 0 {
			fmt.Printf("\rFrame %d/%d", frame, numFrames)
		}
	}

	fmt.Printf("\nClosing FFmpeg...\n")
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("FFmpeg error: %v\n", err)
		os.Exit(1)
	}

	// Print profiling results
	totalTime := time.Since(startTime)
	fmt.Printf("\nPerformance Profile:\n")
	fmt.Printf("  FFT computation:   %v (%.1f%%)\n", totalFFT, float64(totalFFT)/float64(totalTime)*100)
	fmt.Printf("  Bar binning:       %v (%.1f%%)\n", totalBin, float64(totalBin)/float64(totalTime)*100)
	fmt.Printf("  Frame drawing:     %v (%.1f%%)\n", totalDraw, float64(totalDraw)/float64(totalTime)*100)
	fmt.Printf("  FFmpeg writing:    %v (%.1f%%)\n", totalWrite, float64(totalWrite)/float64(totalTime)*100)
	fmt.Printf("  Total time:        %v\n", totalTime)
	fmt.Printf("  Speed:             %.2fx realtime\n", float64(len(samples))/float64(sampleRate)/totalTime.Seconds())

	fmt.Printf("\nDone! Output: %s\n", outputFile)
}

func readWAV(filename string) ([]float64, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := wav.NewDecoder(f)
	if !decoder.IsValidFile() {
		return nil, fmt.Errorf("invalid WAV file")
	}

	buf, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, err
	}

	// Convert to float64
	samples := make([]float64, len(buf.Data))
	for i, s := range buf.Data {
		samples[i] = float64(s) / float64(audio.IntMaxSignedValue(int(decoder.BitDepth)))
	}

	return samples, nil
}

func loadBackgroundImage(filename string) (*image.RGBA, error) {
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

	// Convert to RGBA
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	// If dimensions don't match, scale the image with bilinear interpolation
	if bounds.Dx() != width || bounds.Dy() != height {
		scaleX := float64(bounds.Dx()) / float64(width)
		scaleY := float64(bounds.Dy()) / float64(height)

		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				// Bilinear interpolation
				srcX := float64(x) * scaleX
				srcY := float64(y) * scaleY

				x0 := int(srcX)
				y0 := int(srcY)
				x1 := x0 + 1
				y1 := y0 + 1

				// Clamp to image bounds
				if x1 >= bounds.Dx() {
					x1 = bounds.Dx() - 1
				}
				if y1 >= bounds.Dy() {
					y1 = bounds.Dy() - 1
				}

				// Get the four surrounding pixels
				c00 := img.At(x0, y0)
				c10 := img.At(x1, y0)
				c01 := img.At(x0, y1)
				c11 := img.At(x1, y1)

				// Calculate interpolation weights
				fx := srcX - float64(x0)
				fy := srcY - float64(y0)

				// Convert to RGBA for arithmetic
				r00, g00, b00, a00 := c00.RGBA()
				r10, g10, b10, a10 := c10.RGBA()
				r01, g01, b01, a01 := c01.RGBA()
				r11, g11, b11, a11 := c11.RGBA()

				// Bilinear interpolation formula
				r := uint8((float64(r00>>8)*(1-fx)*(1-fy) +
					float64(r10>>8)*fx*(1-fy) +
					float64(r01>>8)*(1-fx)*fy +
					float64(r11>>8)*fx*fy))

				g := uint8((float64(g00>>8)*(1-fx)*(1-fy) +
					float64(g10>>8)*fx*(1-fy) +
					float64(g01>>8)*(1-fx)*fy +
					float64(g11>>8)*fx*fy))

				b := uint8((float64(b00>>8)*(1-fx)*(1-fy) +
					float64(b10>>8)*fx*(1-fy) +
					float64(b01>>8)*(1-fx)*fy +
					float64(b11>>8)*fx*fy))

				a := uint8((float64(a00>>8)*(1-fx)*(1-fy) +
					float64(a10>>8)*fx*(1-fy) +
					float64(a01>>8)*(1-fx)*fy +
					float64(a11>>8)*fx*fy))

				rgba.SetRGBA(x, y, color.RGBA{R: r, G: g, B: b, A: a})
			}
		}
	} else {
		// Direct copy if dimensions match
		draw.Draw(rgba, rgba.Bounds(), img, bounds.Min, draw.Src)
	}

	return rgba, nil
}

func loadFont(fontPath string, size float64) (font.Face, error) {
	fontBytes, err := os.ReadFile(fontPath)
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

func applyHanning(data []float64) []float64 {
	windowed := make([]float64, len(data))
	n := len(data)
	for i := range data {
		window := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
		windowed[i] = data[i] * window
	}
	return windowed
}

// binFFT bins FFT coefficients into bars and returns NORMALIZED values (0.0-1.0)
// CAVA-style approach: work in normalized space, apply maxBarHeight scaling later
func binFFT(coeffs []complex128, sensitivity float64) []float64 {
	// Use only first half (positive frequencies)
	halfSize := len(coeffs) / 2

	// Focus on frequency range where most audio content is
	// Use first 3/4 of spectrum (0 to ~16.5kHz) for better balance
	// between bass energy and mid/high content
	maxFreqBin := (halfSize * 3) / 4

	barHeights := make([]float64, numBars)
	binsPerBar := maxFreqBin / numBars

	for bar := 0; bar < numBars; bar++ {
		start := bar * binsPerBar
		end := start + binsPerBar
		if end > maxFreqBin {
			end = maxFreqBin
		}

		// Average magnitude in this range
		var sum float64
		for i := start; i < end; i++ {
			magnitude := math.Sqrt(real(coeffs[i])*real(coeffs[i]) + imag(coeffs[i])*imag(coeffs[i]))
			sum += magnitude
		}

		barHeights[bar] = sum / float64(binsPerBar)
	}

	// CAVA-style processing: apply sensitivity, then normalize to 0.0-1.0 range
	// Apply fixed scale factor (tune this based on your audio levels)
	const baseScale = 0.0075 // Base scaling for raw magnitudes

	for i := range barHeights {
		// Apply sensitivity to raw magnitude
		scaled := barHeights[i] * baseScale * sensitivity

		// Noise gate on raw values (before log scale)
		if scaled < 0.01 {
			barHeights[i] = 0
		} else {
			// Log scale for better visual distribution, normalize to ~0.0-1.0
			// Log10(1 + scaled*9) gives range [0, 1] for scaled in [0, 1]
			// We scale up for better dynamic range
			barHeights[i] = math.Log10(1 + scaled*9)

			// DON'T clip here - let overshoot detection in main loop handle it
			// This allows sensitivity adjustment to detect actual overshoots
		}
	}

	return barHeights
}

func rearrangeFrequenciesCenterOut(barHeights []float64) []float64 {
	// Create a symmetric mirror pattern with most active frequencies at CENTER:
	// Left side: frequencies 0→31 placed from CENTER → LEFT EDGE (most active at center)
	// Right side: frequencies 0→31 mirrored from CENTER → RIGHT EDGE
	// Result: Most active (bass) at center, less active (highs) at edges

	n := len(barHeights)
	rearranged := make([]float64, n)
	center := n / 2

	// Place first half of frequencies mirrored from center outward
	for i := 0; i < n/2; i++ {
		// Left side: place from center going left (most active near center)
		rearranged[center-1-i] = barHeights[i]
		// Right side: mirror (most active near center)
		rearranged[center+i] = barHeights[i]
	}

	return rearranged
}

func drawCenterText(img *image.RGBA, face font.Face, text string, centerY int) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 248, G: 179, B: 29, A: 255}), // #F8B31D (brand yellow)
		Face: face,
	}

	// Measure text width
	bounds, _ := d.BoundString(text)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()

	// Calculate centered position
	x := (width - textWidth) / 2
	y := centerY + 10 // Slightly below center for better visual alignment

	d.Dot = freetype.Pt(x, y)
	d.DrawString(text)
}

func drawEpisodeNumber(img *image.RGBA, face font.Face, episodeNum string) {
	// Create a drawer
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.RGBA{R: 248, G: 179, B: 29, A: 255}), // #F8B31D (brand yellow)
		Face: face,
	}

	// Measure text dimensions
	bounds, _ := d.BoundString(episodeNum)
	textWidth := (bounds.Max.X - bounds.Min.X).Ceil()
	textHeight := (bounds.Max.Y - bounds.Min.Y).Ceil()

	// Position in top right corner with proportional offset (40px from edges)
	offset := 30
	x := width - textWidth - offset
	y := textHeight + offset

	d.Dot = freetype.Pt(x, y)
	d.DrawString(episodeNum)
}

func drawFrame(barHeights []float64, img *image.RGBA, bgImage *image.RGBA, fontFace font.Face) {
	if bgImage != nil {
		// Copy background image
		draw.Draw(img, img.Bounds(), bgImage, image.Point{0, 0}, draw.Src)
	} else {
		// Fast clear to black - memset style
		for i := 0; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0     // R
			img.Pix[i+1] = 0   // G
			img.Pix[i+2] = 0   // B
			img.Pix[i+3] = 255 // A
		}
	}

	// Center point
	centerY := height / 2

	// Calculate starting position to center all bars
	totalWidth := numBars*barWidth + (numBars-1)*barGap
	startX := (width - totalWidth) / 2

	for i, h := range barHeights {
		barHeight := int(h)
		x := startX + i*(barWidth+barGap)
		if x+barWidth > width {
			continue
		}

		// Draw bar upward from center (with gap/2 offset) with subtle alpha gradient
		for y := centerY - barHeight - centerGap/2; y < centerY-centerGap/2; y++ {
			if y >= 0 && y < height {
				// Calculate distance from center (0.0 at center, 1.0 at tip)
				distanceFromCenter := float64(centerY-centerGap/2-y) / float64(barHeight)
				// Gradient: 1.0 (full) at center to 0.5 (50%) at tip
				alphaFactor := 1.0 - (distanceFromCenter * 0.5)

				offset := y*img.Stride + x*4
				for px := 0; px < barWidth; px++ {
					pixOffset := offset + px*4
					// Get background color
					bgR := img.Pix[pixOffset]
					bgG := img.Pix[pixOffset+1]
					bgB := img.Pix[pixOffset+2]

					// Alpha blend: result = bar*alpha + bg*(1-alpha)
					img.Pix[pixOffset] = uint8(float64(barColorR)*alphaFactor + float64(bgR)*(1.0-alphaFactor))
					img.Pix[pixOffset+1] = uint8(float64(barColorG)*alphaFactor + float64(bgG)*(1.0-alphaFactor))
					img.Pix[pixOffset+2] = uint8(float64(barColorB)*alphaFactor + float64(bgB)*(1.0-alphaFactor))
					img.Pix[pixOffset+3] = 255
				}
			}
		}

		// Draw mirror bar downward from center (with gap/2 offset) with subtle alpha gradient
		for y := centerY + centerGap/2; y < centerY+barHeight+centerGap/2; y++ {
			if y >= 0 && y < height {
				// Calculate distance from center (0.0 at center, 1.0 at tip)
				distanceFromCenter := float64(y-(centerY+centerGap/2)) / float64(barHeight)
				// Gradient: 1.0 (full) at center to 0.5 (50%) at tip
				alphaFactor := 1.0 - (distanceFromCenter * 0.5)

				offset := y*img.Stride + x*4
				for px := 0; px < barWidth; px++ {
					pixOffset := offset + px*4
					// Get background color
					bgR := img.Pix[pixOffset]
					bgG := img.Pix[pixOffset+1]
					bgB := img.Pix[pixOffset+2]

					// Alpha blend: result = bar*alpha + bg*(1-alpha)
					img.Pix[pixOffset] = uint8(float64(barColorR)*alphaFactor + float64(bgR)*(1.0-alphaFactor))
					img.Pix[pixOffset+1] = uint8(float64(barColorG)*alphaFactor + float64(bgG)*(1.0-alphaFactor))
					img.Pix[pixOffset+2] = uint8(float64(barColorB)*alphaFactor + float64(bgB)*(1.0-alphaFactor))
					img.Pix[pixOffset+3] = 255
				}
			}
		}
	}

	// Draw center text and episode number on top of bars
	if fontFace != nil {
		drawCenterText(img, fontFace, "Linux Matters Sample Text", centerY)
		drawEpisodeNumber(img, fontFace, "00")
	}
}

func writeRawRGB(w io.WriteCloser, img *image.RGBA) {
	// Write raw RGB24 data directly from pixel buffer
	// This is MUCH faster than img.At() which does bounds checking and color conversion
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

func generateSnapshot(samples []float64, outputFile string, atTime float64) {
	fmt.Printf("Generating snapshot at %.2f seconds...\n", atTime)

	// Calculate the frame position
	samplesPerFrame := sampleRate / fps
	frameNumber := int(atTime * float64(fps))
	start := frameNumber * samplesPerFrame
	end := start + fftSize

	if start >= len(samples) {
		fmt.Printf("Error: timestamp %.2f is beyond audio duration\n", atTime)
		os.Exit(1)
	}

	if end > len(samples) {
		end = len(samples)
	}

	// Get FFT of this chunk
	chunk := samples[start:end]
	if len(chunk) < fftSize {
		padded := make([]float64, fftSize)
		copy(padded, chunk)
		chunk = padded
	}

	// Apply Hanning window
	windowed := applyHanning(chunk)

	// Compute FFT
	fft := fourier.NewFFT(fftSize)
	coeffs := fft.Coefficients(nil, windowed)

	// Compute magnitudes and bin into bars (sensitivity=1.0 for snapshot)
	barHeights := binFFT(coeffs, 1.0)

	// Load background image
	bgImage, err := loadBackgroundImage("bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load bg.png: %v\n", err)
		bgImage = nil
	}

	// Load font
	fontFace, err := loadFont("Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fontFace = nil
	}

	// Create image
	img := image.NewRGBA(image.Rect(0, 0, width, height))

	// Rearrange frequencies for center-out symmetric distribution
	rearrangedHeights := rearrangeFrequenciesCenterOut(barHeights)

	// Draw frame
	drawFrame(rearrangedHeights, img, bgImage, fontFace)

	// Save as PNG
	f, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		fmt.Printf("Error encoding PNG: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Snapshot saved to: %s\n", outputFile)
}
