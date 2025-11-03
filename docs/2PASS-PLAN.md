# 📋 **2-Pass Implementation Plan: Detailed Steps**

## **Overview**

Convert from single-pass (load all samples) to 2-pass (analysis → rendering) architecture with streaming reads.

**Memory Impact:** 600MB → ~50MB for 30-minute audio (92% reduction)

---

## **Phase 1: Add Streaming Infrastructure** ⚙️

### **Step 1.1: Create Streaming WAV Reader**

**File:** reader.go

**Add new function** (keep existing `ReadWAV` for now):

```go
// StreamingReader provides chunk-based WAV reading
type StreamingReader struct {
	decoder    *wav.Decoder
	file       *os.File
	sampleRate int
	bitDepth   int
	numSamples int64
	position   int64
}

// NewStreamingReader creates a streaming WAV reader
func NewStreamingReader(filename string) (*StreamingReader, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	decoder := wav.NewDecoder(f)
	if !decoder.IsValidFile() {
		f.Close()
		return nil, fmt.Errorf("invalid WAV file")
	}

	// Get format info without reading all samples
	decoder.ReadInfo()

	return &StreamingReader{
		decoder:    decoder,
		file:       f,
		sampleRate: int(decoder.SampleRate),
		bitDepth:   int(decoder.BitDepth),
		numSamples: int64(decoder.NumChunks) * int64(decoder.Format.NumChannels),
		position:   0,
	}, nil
}

// ReadChunk reads next chunk of samples, returns nil when EOF
func (r *StreamingReader) ReadChunk(numSamples int) ([]float64, error) {
	if r.position >= r.numSamples {
		return nil, io.EOF
	}

	// Create buffer for reading
	intBuf := &audio.IntBuffer{
		Data:   make([]int, numSamples),
		Format: r.decoder.Format,
	}

	n, err := r.decoder.PCMBuffer(intBuf)
	if err != nil && err != io.EOF {
		return nil, err
	}

	if n == 0 {
		return nil, io.EOF
	}

	// Convert to float64
	samples := make([]float64, n)
	maxVal := float64(audio.IntMaxSignedValue(r.bitDepth))
	for i := 0; i < n; i++ {
		samples[i] = float64(intBuf.Data[i]) / maxVal
	}

	r.position += int64(n)
	return samples, nil
}

// Seek repositions reader to sample position
func (r *StreamingReader) Seek(samplePos int64) error {
	r.position = samplePos
	// Seek in file (WAV has 44-byte header + data)
	bytePos := 44 + (samplePos * int64(r.bitDepth/8))
	_, err := r.file.Seek(bytePos, io.SeekStart)
	return err
}

// Close closes the underlying file
func (r *StreamingReader) Close() error {
	return r.file.Close()
}

// NumSamples returns total sample count
func (r *StreamingReader) NumSamples() int64 {
	return r.numSamples
}

// SampleRate returns the sample rate
func (r *StreamingReader) SampleRate() int {
	return r.sampleRate
}
```

---

## **Phase 2: Create Analysis Infrastructure** 🔍

### **Step 2.1: Define Data Structures**

**File:** `internal/audio/analyzer.go` (NEW FILE)

```go
package audio

import (
	"fmt"
	"math"

	"github.com/linuxmatters/jivefire/internal/config"
)

// FrameAnalysis holds statistics for a single frame
type FrameAnalysis struct {
	// Peak FFT magnitude across all bars
	PeakMagnitude float64

	// RMS level of audio chunk
	RMSLevel float64

	// Average per-bar magnitudes (for future use)
	BarMagnitudes [config.NumBars]float64
}

// AudioProfile holds complete audio analysis results
type AudioProfile struct {
	// Total number of frames in audio
	NumFrames int

	// Per-frame analysis data
	Frames []FrameAnalysis

	// Global statistics
	GlobalPeak      float64 // Highest peak magnitude across all frames
	GlobalRMS       float64 // Average RMS across all frames
	DynamicRange    float64 // Ratio of GlobalPeak to GlobalRMS

	// Calculated optimal parameters
	OptimalBaseScale float64 // Replaces hardcoded 0.0075

	// Audio metadata
	SampleRate   int
	Duration     float64 // Seconds
}
```

### **Step 2.2: Implement Analysis Pass**

**File:** `internal/audio/analyzer.go` (continue)

```go
// AnalyzeAudio performs Pass 1: stream through audio and collect statistics
func AnalyzeAudio(filename string) (*AudioProfile, error) {
	fmt.Printf("Pass 1: Analyzing audio...\n")

	// Open streaming reader
	reader, err := NewStreamingReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open WAV: %w", err)
	}
	defer reader.Close()

	// Calculate number of frames
	samplesPerFrame := config.SampleRate / config.FPS
	numFrames := int(reader.NumSamples()) / samplesPerFrame
	duration := float64(reader.NumSamples()) / float64(reader.SampleRate())

	profile := &AudioProfile{
		NumFrames:  numFrames,
		Frames:     make([]FrameAnalysis, numFrames),
		SampleRate: reader.SampleRate(),
		Duration:   duration,
	}

	// Create FFT processor
	processor := NewProcessor()

	// Analyze each frame
	var sumRMS float64
	var maxPeak float64

	for frameNum := 0; frameNum < numFrames; frameNum++ {
		// Read chunk for FFT (need FFTSize samples)
		chunk, err := reader.ReadChunk(config.FFTSize)
		if err != nil {
			break // End of audio
		}

		// Pad if needed
		if len(chunk) < config.FFTSize {
			padded := make([]float64, config.FFTSize)
			copy(padded, chunk)
			chunk = padded
		}

		// Compute FFT
		coeffs := processor.ProcessChunk(chunk)

		// Analyze frequency bins
		analysis := analyzeFrame(coeffs, chunk)
		profile.Frames[frameNum] = analysis

		// Track global statistics
		if analysis.PeakMagnitude > maxPeak {
			maxPeak = analysis.PeakMagnitude
		}
		sumRMS += analysis.RMSLevel

		// Progress indicator
		if frameNum%100 == 0 {
			progress := float64(frameNum) / float64(numFrames) * 100
			fmt.Printf("\r  Analyzing: %.1f%%", progress)
		}

		// Advance to next frame position
		// Skip ahead by samplesPerFrame - FFTSize (since we already read FFTSize)
		skipSamples := samplesPerFrame - config.FFTSize
		if skipSamples > 0 {
			_, _ = reader.ReadChunk(skipSamples)
		}
	}

	fmt.Printf("\r  Analyzing: 100.0%%\n")

	// Calculate global statistics
	profile.GlobalPeak = maxPeak
	profile.GlobalRMS = sumRMS / float64(numFrames)
	profile.DynamicRange = profile.GlobalPeak / profile.GlobalRMS

	// Calculate optimal baseScale
	// Goal: GlobalPeak should map to ~0.8-0.9 in normalized space
	// Current formula: scaled = magnitude * baseScale * sensitivity
	// We want: GlobalPeak * baseScale * 1.0 ≈ 0.85
	profile.OptimalBaseScale = 0.85 / profile.GlobalPeak

	fmt.Printf("  Audio Profile:\n")
	fmt.Printf("    Duration:      %.1f seconds\n", profile.Duration)
	fmt.Printf("    Frames:        %d\n", profile.NumFrames)
	fmt.Printf("    Global Peak:   %.6f\n", profile.GlobalPeak)
	fmt.Printf("    Global RMS:    %.6f\n", profile.GlobalRMS)
	fmt.Printf("    Dynamic Range: %.2f\n", profile.DynamicRange)
	fmt.Printf("    Optimal Scale: %.6f\n", profile.OptimalBaseScale)

	return profile, nil
}

// analyzeFrame extracts statistics from FFT coefficients and audio chunk
func analyzeFrame(coeffs []complex128, audioChunk []float64) FrameAnalysis {
	analysis := FrameAnalysis{}

	// Calculate RMS of audio chunk
	var sumSquares float64
	for _, sample := range audioChunk {
		sumSquares += sample * sample
	}
	analysis.RMSLevel = math.Sqrt(sumSquares / float64(len(audioChunk)))

	// Analyze frequency bins (same logic as BinFFT)
	halfSize := len(coeffs) / 2
	maxFreqBin := (halfSize * 3) / 4
	binsPerBar := maxFreqBin / config.NumBars

	for bar := 0; bar < config.NumBars; bar++ {
		start := bar * binsPerBar
		end := start + binsPerBar
		if end > maxFreqBin {
			end = maxFreqBin
		}

		var sum float64
		for i := start; i < end; i++ {
			magnitude := math.Sqrt(real(coeffs[i])*real(coeffs[i]) + imag(coeffs[i])*imag(coeffs[i]))
			sum += magnitude
		}

		avgMagnitude := sum / float64(binsPerBar)
		analysis.BarMagnitudes[bar] = avgMagnitude

		// Track peak
		if avgMagnitude > analysis.PeakMagnitude {
			analysis.PeakMagnitude = avgMagnitude
		}
	}

	return analysis
}
```

---

## **Phase 3: Modify FFT Processing** 🎛️

### **Step 3.1: Update BinFFT to Accept BaseScale**

**File:** fft.go

**Change signature and remove const:**

```go
// BinFFT bins FFT coefficients into bars and returns normalized values (0.0-1.0)
// CAVA-style approach: work in normalized space, apply maxBarHeight scaling later
func BinFFT(coeffs []complex128, sensitivity float64, baseScale float64) []float64 {
	// Use only first half (positive frequencies)
	halfSize := len(coeffs) / 2

	// Focus on frequency range where most audio content is
	// Use first 3/4 of spectrum (0 to ~16.5kHz) for better balance
	// between bass energy and mid/high content
	maxFreqBin := (halfSize * 3) / 4

	barHeights := make([]float64, config.NumBars)
	binsPerBar := maxFreqBin / config.NumBars

	for bar := 0; bar < config.NumBars; bar++ {
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

	// CAVA-style processing: apply sensitivity and baseScale
	// baseScale is now calculated from audio analysis (Pass 1)
	for i := range barHeights {
		// Apply sensitivity and baseScale to raw magnitude
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
```

---

## **Phase 4: Refactor Main Application** 🔧

### **Step 4.1: Update generateVideo Function**

**File:** main.go

**Replace the entire `generateVideo` function:**

```go
func generateVideo(samples []float64, inputFile string, outputFile string) {
	// This function signature kept for compatibility, but samples param unused
	// TODO: Remove samples parameter after full refactor

	// === PASS 1: ANALYZE AUDIO ===
	profile, err := audio.AnalyzeAudio(inputFile)
	if err != nil {
		fmt.Printf("Error analyzing audio: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nPass 2: Rendering visualization...\n")

	// Start FFmpeg process
	cmd := exec.Command("ffmpeg",
		"-y",
		"-f", "rawvideo",
		"-pixel_format", "rgb24",
		"-video_size", fmt.Sprintf("%dx%d", config.Width, config.Height),
		"-framerate", fmt.Sprintf("%d", config.FPS),
		"-i", "pipe:0",
		"-i", inputFile,
		"-c:v", "libx264",
		"-preset", "ultrafast",
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

	// Load assets
	bgImage, err := renderer.LoadBackgroundImage("assets/bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load assets/bg.png: %v\n", err)
		bgImage = nil
	}

	fontFace, err := renderer.LoadFont("assets/Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fontFace = nil
	}

	// === PASS 2: STREAM AND RENDER ===
	reader, err := audio.NewStreamingReader(inputFile)
	if err != nil {
		fmt.Printf("Error opening audio for rendering: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	// Create processor and renderer
	processor := audio.NewProcessor()
	frame := renderer.NewFrame(bgImage, fontFace)

	// Profiling
	var totalFFT, totalBin, totalDraw, totalWrite time.Duration
	startTime := time.Now()

	// CAVA algorithm state
	samplesPerFrame := config.SampleRate / config.FPS
	prevBarHeights := make([]float64, config.NumBars)
	cavaPeaks := make([]float64, config.NumBars)
	cavaFall := make([]float64, config.NumBars)
	cavaMem := make([]float64, config.NumBars)

	gravityMod := math.Pow(60.0/config.Framerate, 2.5) * 1.54 / config.NoiseReduction
	if gravityMod < 1.0 {
		gravityMod = 1.0
	}

	// Start with optimal sensitivity (can still adapt if needed)
	sensitivity := 1.0

	// Render each frame
	for frameNum := 0; frameNum < profile.NumFrames; frameNum++ {
		// Read chunk for this frame
		chunk, err := reader.ReadChunk(config.FFTSize)
		if err != nil {
			break
		}

		// Pad if needed
		if len(chunk) < config.FFTSize {
			padded := make([]float64, config.FFTSize)
			copy(padded, chunk)
			chunk = padded
		}

		// Compute FFT
		t0 := time.Now()
		coeffs := processor.ProcessChunk(chunk)
		totalFFT += time.Since(t0)

		// Bin with optimal baseScale from analysis
		t0 = time.Now()
		barHeights := audio.BinFFT(coeffs, sensitivity, profile.OptimalBaseScale)
		totalBin += time.Since(t0)

		// CAVA-style auto-sensitivity (less work needed now!)
		overshootDetected := false
		for i, h := range barHeights {
			if h > 1.0 {
				overshootDetected = true
				overshoot := h - 1.0
				barHeights[i] = 1.0 + overshoot*math.Exp(-overshoot)
			}
		}

		if overshootDetected {
			sensitivity *= 0.985
		} else {
			sensitivity *= 1.002
		}

		if sensitivity < 0.05 {
			sensitivity = 0.05
		}
		if sensitivity > 2.0 {
			sensitivity = 2.0
		}

		// Scale to pixel space
		actualAvailableSpace := float64(config.Height/2 - config.CenterGap/2)
		availableHeight := actualAvailableSpace * config.MaxBarHeight
		for i := range barHeights {
			barHeights[i] *= availableHeight
		}

		// Apply CAVA gravity smoothing
		for i := range barHeights {
			currentHeight := barHeights[i]

			if currentHeight < prevBarHeights[i] {
				currentHeight = cavaPeaks[i] * (1.0 - (cavaFall[i] * cavaFall[i] * gravityMod))
				cavaFall[i] += config.FallAccel

				if currentHeight < 0 {
					currentHeight = 0
				}
			} else {
				cavaPeaks[i] = currentHeight
				cavaFall[i] = 0.0
			}

			currentHeight = cavaMem[i]*config.NoiseReduction + currentHeight
			cavaMem[i] = currentHeight

			if currentHeight > availableHeight {
				overshoot := currentHeight - availableHeight
				currentHeight = availableHeight + overshoot*math.Exp(-overshoot/availableHeight)
			}

			prevBarHeights[i] = currentHeight
		}

		// Rearrange and render
		rearrangedHeights := audio.RearrangeFrequenciesCenterOut(prevBarHeights)

		t0 = time.Now()
		frame.Draw(rearrangedHeights)
		totalDraw += time.Since(t0)

		t0 = time.Now()
		renderer.WriteRawRGB(stdin, frame.GetImage())
		totalWrite += time.Since(t0)

		if frameNum%30 == 0 {
			fmt.Printf("\r  Rendering: %d/%d frames", frameNum, profile.NumFrames)
		}

		// Skip to next frame position
		skipSamples := samplesPerFrame - config.FFTSize
		if skipSamples > 0 {
			_, _ = reader.ReadChunk(skipSamples)
		}
	}

	fmt.Printf("\r  Rendering: %d/%d frames\n", profile.NumFrames, profile.NumFrames)
	fmt.Printf("Closing FFmpeg...\n")
	stdin.Close()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("FFmpeg error: %v\n", err)
		os.Exit(1)
	}

	// Print profiling
	totalTime := time.Since(startTime)
	fmt.Printf("\nPerformance Profile:\n")
	fmt.Printf("  FFT computation:   %v (%.1f%%)\n", totalFFT, float64(totalFFT)/float64(totalTime)*100)
	fmt.Printf("  Bar binning:       %v (%.1f%%)\n", totalBin, float64(totalBin)/float64(totalTime)*100)
	fmt.Printf("  Frame drawing:     %v (%.1f%%)\n", totalDraw, float64(totalDraw)/float64(totalTime)*100)
	fmt.Printf("  FFmpeg writing:    %v (%.1f%%)\n", totalWrite, float64(totalWrite)/float64(totalTime)*100)
	fmt.Printf("  Total time:        %v\n", totalTime)
	fmt.Printf("  Speed:             %.2fx realtime\n", profile.Duration/totalTime.Seconds())

	fmt.Printf("\nDone! Output: %s\n", outputFile)
}
```

### **Step 4.2: Update generateSnapshot Function**

**File:** main.go

```go
func generateSnapshot(samples []float64, outputFile string, atTime float64) {
	// Note: Still uses samples array for simplicity in snapshot mode
	// Could be refactored to streaming later if needed

	fmt.Printf("Generating snapshot at %.2f seconds...\n", atTime)

	samplesPerFrame := config.SampleRate / config.FPS
	frameNumber := int(atTime * float64(config.FPS))
	start := frameNumber * samplesPerFrame
	end := start + config.FFTSize

	if start >= len(samples) {
		fmt.Printf("Error: timestamp %.2f is beyond audio duration\n", atTime)
		os.Exit(1)
	}

	if end > len(samples) {
		end = len(samples)
	}

	chunk := samples[start:end]

	processor := audio.NewProcessor()
	coeffs := processor.ProcessChunk(chunk)

	// Use default baseScale for snapshot (or could analyze first)
	const defaultBaseScale = 0.0075
	barHeights := audio.BinFFT(coeffs, 1.0, defaultBaseScale)

	// Scale to pixel space
	actualAvailableSpace := float64(config.Height/2 - config.CenterGap/2)
	availableHeight := actualAvailableSpace * config.MaxBarHeight
	for i := range barHeights {
		barHeights[i] *= availableHeight
	}

	bgImage, err := renderer.LoadBackgroundImage("assets/bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load assets/bg.png: %v\n", err)
		bgImage = nil
	}

	fontFace, err := renderer.LoadFont("assets/Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fontFace = nil
	}

	frame := renderer.NewFrame(bgImage, fontFace)
	rearrangedHeights := audio.RearrangeFrequenciesCenterOut(barHeights)
	frame.Draw(rearrangedHeights)

	f, err := os.Create(outputFile)
	if err != nil {
		fmt.Printf("Error creating output file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if err := png.Encode(f, frame.GetImage()); err != nil {
		fmt.Printf("Error encoding PNG: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Snapshot saved to: %s\n", outputFile)
}
```

---

## **Phase 5: Update Main Entry Point** 🚀

### **Step 5.1: Modify main() to Remove Full Load**

**File:** main.go

```go
func main() {
	ctx := kong.Parse(&CLI,
		kong.Name("jivefire"),
		kong.Description("Spin your podcast .wav into a groovy MP4 visualiser. Cava-inspired audio frequencies dancing in real-time."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
	)

	if CLI.Version {
		fmt.Printf("jivefire version %s\n", version)
		os.Exit(0)
	}

	if CLI.Input == "" || CLI.Output == "" {
		fmt.Fprintln(os.Stderr, "Error: <input> and <output> are required")
		os.Exit(1)
	}

	inputFile := CLI.Input
	outputFile := CLI.Output
	snapshotMode := CLI.Snapshot != nil
	snapshotTime := 1.0
	if snapshotMode {
		snapshotTime = *CLI.Snapshot
	}

	_ = ctx

	// SNAPSHOT MODE: Still uses old approach for simplicity
	if snapshotMode {
		fmt.Printf("Reading audio: %s\n", inputFile)
		samples, err := audio.ReadWAV(inputFile)
		if err != nil {
			fmt.Printf("Error reading WAV: %v\n", err)
			os.Exit(1)
		}
		generateSnapshot(samples, outputFile, snapshotTime)
		return
	}

	// VIDEO MODE: Uses new 2-pass streaming approach
	generateVideo(nil, inputFile, outputFile) // Pass nil for samples - not used anymore
}
```

---

## **Phase 6: Cleanup** 🧹

### **Step 6.1: Update Config**

**File:** config.go

**Remove the baseScale constant** (it's now calculated per-audio):

```go
// Remove or comment out:
// const baseScale = 0.0075
```

### **Step 6.2: Update Documentation**

**File:** ORIGINAL-CONCEPT.md or README.md

Add section explaining 2-pass architecture:

```markdown
## Architecture Update: 2-Pass Processing

**Pass 1: Analysis**
- Streams through audio once
- Collects per-frame statistics (peak, RMS, frequency distribution)
- Calculates optimal baseScale for this specific audio
- Memory: ~50MB for 30-minute podcast (metadata table)

**Pass 2: Rendering**
- Streams through audio again
- Applies optimal scaling from Pass 1
- Renders frames with perfect normalization
- No clipping, no adaptation lag

**Benefits:**
- 92% memory reduction (600MB → 50MB for 30-min audio)
- Eliminates hardcoded `baseScale = 0.0075` magic number
- Perfect scaling for each podcast's unique audio characteristics
- Enables future enhancements (silence detection, beat sync)
```

---

## **Testing Strategy** ✅

### **Step 7.1: Unit Tests**

Create `internal/audio/analyzer_test.go`:

```go
func TestAnalyzeAudio(t *testing.T) {
	profile, err := AnalyzeAudio("../../testdata/dream.wav")
	assert.NoError(t, err)
	assert.Greater(t, profile.NumFrames, 0)
	assert.Greater(t, profile.GlobalPeak, 0.0)
	assert.Greater(t, profile.OptimalBaseScale, 0.0)
}

func TestStreamingReader(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	assert.NoError(t, err)
	defer reader.Close()

	chunk, err := reader.ReadChunk(2048)
	assert.NoError(t, err)
	assert.Equal(t, 2048, len(chunk))
}
```

### **Step 7.2: Integration Test**

```bash
# Test video generation
just build
./jivefire testdata/dream.wav testdata/test-2pass.mp4

# Compare output visually with old version
# Check that performance is still good (~9x realtime or better)
```

---

## **Implementation Order** 📅

1. ✅ **Day 1:** Implement `StreamingReader` in reader.go + unit tests
2. ✅ **Day 2:** Create `analyzer.go` with `AnalyzeAudio()` + unit tests
3. ✅ **Day 3:** Update `BinFFT()` to accept `baseScale` parameter
4. ✅ **Day 4:** Refactor `generateVideo()` to use 2-pass approach
5. ✅ **Day 5:** Test, validate, compare outputs with current version
6. ✅ **Day 6:** Update docs, remove old `ReadWAV` usage (keep function for snapshot mode)
7. ✅ **Day 7:** Consider refactoring snapshot mode to streaming (optional)

---

## **Expected Outcomes** 🎯

- ✅ Memory usage: ~50MB instead of 600MB for 30-minute audio
- ✅ No hardcoded `baseScale` - calculated per audio
- ✅ Better visual quality - optimal scaling from frame 1
- ✅ Performance: Similar or better (~9x realtime maintained)
- ✅ Foundation for future enhancements (silence detection, beat sync)

---
