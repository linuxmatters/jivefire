package main

import (
	"fmt"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"time"

	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jivefire/internal/audio"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/renderer"
)

const version = "0.0.1"

var CLI struct {
	Input    string   `arg:"" name:"input" help:"Input WAV file" type:"existingfile" optional:""`
	Output   string   `arg:"" name:"output" help:"Output file (.mp4 for video, .png for snapshot)" optional:""`
	Snapshot *float64 `help:"Generate snapshot at specified time (seconds) instead of full video" short:"s"`
	Version  bool     `help:"Show version information" short:"v"`
}

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

	// Handle version flag
	if CLI.Version {
		fmt.Printf("jivefire version %s\n", version)
		os.Exit(0)
	}

	// Validate required arguments when not showing version
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

	_ = ctx // Kong context available for future use

	if snapshotMode {
		// Snapshot mode: use old single-pass approach for simplicity
		fmt.Printf("Reading audio: %s\n", inputFile)
		samples, err := audio.ReadWAV(inputFile)
		if err != nil {
			fmt.Printf("Error reading WAV: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Loaded %d samples\n", len(samples))
		
		generateSnapshot(samples, outputFile, snapshotTime)
		return
	}

	// Video mode: use 2-pass streaming approach
	generateVideo(inputFile, outputFile)
}

func generateVideo(inputFile string, outputFile string) {
	// ============================================================================
	// PASS 1: Analyze audio to calculate optimal parameters
	// ============================================================================
	profile, err := audio.AnalyzeAudio(inputFile)
	if err != nil {
		fmt.Printf("Error analyzing audio: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nPass 2: Rendering video...\n")

	// Open streaming reader for Pass 2
	reader, err := audio.NewStreamingReader(inputFile)
	if err != nil {
		fmt.Printf("Error opening audio stream: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	// Start FFmpeg process with optimized settings
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

	// Load background image
	bgImage, err := renderer.LoadBackgroundImage("assets/bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load assets/bg.png: %v\n", err)
		fmt.Printf("Continuing with black background...\n")
		bgImage = nil
	}

	// Load font for center text
	fontFace, err := renderer.LoadFont("assets/Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fmt.Printf("Continuing without text...\n")
		fontFace = nil
	}

	// Create audio processor and frame renderer
	processor := audio.NewProcessor()
	frame := renderer.NewFrame(bgImage, fontFace)

	// Calculate frames from profile
	numFrames := profile.NumFrames

	// Profiling variables
	var totalFFT, totalBin, totalDraw, totalWrite time.Duration
	startTime := time.Now()

	// CAVA algorithm state
	prevBarHeights := make([]float64, config.NumBars)
	cavaPeaks := make([]float64, config.NumBars)
	cavaFall := make([]float64, config.NumBars)
	cavaMem := make([]float64, config.NumBars)

	// Calculate gravity modifier (CAVA formula)
	gravityMod := math.Pow(60.0/config.Framerate, 2.5) * 1.54 / config.NoiseReduction
	if gravityMod < 1.0 {
		gravityMod = 1.0
	}

	// Auto-sensitivity adjustment (CAVA-style)
	sensitivity := 1.0

	for frameNum := 0; frameNum < numFrames; frameNum++ {
		// Read next chunk from streaming reader
		chunk, err := reader.ReadChunk(config.FFTSize)
		if err == io.EOF {
			break // End of audio
		}
		if err != nil {
			fmt.Printf("\nError reading audio chunk: %v\n", err)
			break
		}

		// Compute FFT
		t0 := time.Now()
		coeffs := processor.ProcessChunk(chunk)
		totalFFT += time.Since(t0)

		// Compute magnitudes and bin into bars using optimal baseScale from Pass 1
		t0 = time.Now()
		barHeights := audio.BinFFT(coeffs, sensitivity, profile.OptimalBaseScale)
		totalBin += time.Since(t0)

		// CAVA-style auto-sensitivity with soft knee compression
		const overshootThreshold = 1.0
		overshootDetected := false

		for i, h := range barHeights {
			if h > overshootThreshold {
				overshootDetected = true
				// Apply soft knee compression
				overshoot := h - overshootThreshold
				barHeights[i] = overshootThreshold + overshoot*math.Exp(-overshoot/overshootThreshold)
			}
		}

		// Adjust sensitivity
		if overshootDetected {
			sensitivity *= 0.985 // 1.5% reduction per frame
		} else {
			sensitivity *= 1.002 // 0.2% increase per frame
		}

		// Clamp sensitivity
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

		// Apply CAVA-style gravity smoothing
		for i := range barHeights {
			currentHeight := barHeights[i]

			if currentHeight < prevBarHeights[i] {
				// Falling: apply gravity with quadratic acceleration
				currentHeight = cavaPeaks[i] * (1.0 - (cavaFall[i] * cavaFall[i] * gravityMod))
				cavaFall[i] += config.FallAccel

				if currentHeight < 0 {
					currentHeight = 0
				}
			} else {
				// Rising: new peak
				cavaPeaks[i] = currentHeight
				cavaFall[i] = 0.0
			}

			// CAVA integral smoothing
			currentHeight = cavaMem[i]*config.NoiseReduction + currentHeight
			cavaMem[i] = currentHeight

			// Soft knee compression
			if currentHeight > availableHeight {
				overshoot := currentHeight - availableHeight
				currentHeight = availableHeight + overshoot*math.Exp(-overshoot/availableHeight)
			}

			prevBarHeights[i] = currentHeight
		}

		// Rearrange frequencies for center-out distribution
		rearrangedHeights := audio.RearrangeFrequenciesCenterOut(prevBarHeights)

		// Generate frame image
		t0 = time.Now()
		frame.Draw(rearrangedHeights)
		totalDraw += time.Since(t0)

		// Write raw RGB to FFmpeg
		t0 = time.Now()
		renderer.WriteRawRGB(stdin, frame.GetImage())
		totalWrite += time.Since(t0)

		if frameNum%30 == 0 {
			fmt.Printf("\rFrame %d/%d", frameNum, numFrames)
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
	fmt.Printf("  Speed:             %.2fx realtime\n", profile.Duration/totalTime.Seconds())

	fmt.Printf("\nDone! Output: %s\n", outputFile)
}

func generateSnapshot(samples []float64, outputFile string, atTime float64) {
	fmt.Printf("Generating snapshot at %.2f seconds...\n", atTime)

	// Calculate the frame position
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

	// Get FFT of this chunk
	chunk := samples[start:end]

	// Create audio processor
	processor := audio.NewProcessor()
	coeffs := processor.ProcessChunk(chunk)

	// Compute magnitudes and bin into bars
	// TODO Phase 4: Replace with profile.OptimalBaseScale from Pass 1 analysis
	const baseScale = 0.0075 // Temporary: will be replaced with calculated value
	barHeights := audio.BinFFT(coeffs, 1.0, baseScale)

	// Load background image
	bgImage, err := renderer.LoadBackgroundImage("assets/bg.png")
	if err != nil {
		fmt.Printf("Warning: Could not load assets/bg.png: %v\n", err)
		bgImage = nil
	}

	// Load font
	fontFace, err := renderer.LoadFont("assets/Poppins-Regular.ttf", 48)
	if err != nil {
		fmt.Printf("Warning: Could not load Poppins-Regular.ttf: %v\n", err)
		fontFace = nil
	}

	// Create frame
	frame := renderer.NewFrame(bgImage, fontFace)

	// Rearrange frequencies
	rearrangedHeights := audio.RearrangeFrequenciesCenterOut(barHeights)

	// Draw frame
	frame.Draw(rearrangedHeights)

	// Save as PNG
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
