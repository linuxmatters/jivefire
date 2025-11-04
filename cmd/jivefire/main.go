package main

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"os"
	"time"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/linuxmatters/jivefire/internal/audio"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/encoder"
	"github.com/linuxmatters/jivefire/internal/renderer"
	"github.com/linuxmatters/jivefire/internal/ui"
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

	// Create Bubbletea program for Pass 1
	model := ui.NewPass1Model()
	p := tea.NewProgram(model)

	// Run analysis in a goroutine and send progress updates
	var profile *audio.AudioProfile
	var analysisErr error

	go func() {
		profile, analysisErr = audio.AnalyzeAudio(inputFile, func(frame, totalFrames int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration) {
			// Send progress update to Bubbletea
			p.Send(ui.Pass1Progress{
				Frame:       frame,
				TotalFrames: totalFrames,
				CurrentRMS:  currentRMS,
				CurrentPeak: currentPeak,
				BarHeights:  barHeights,
				Duration:    duration,
			})
		})

		// Send completion message
		if analysisErr == nil {
			p.Send(ui.Pass1Complete{
				PeakMagnitude: profile.GlobalPeak,
				RMSLevel:      profile.GlobalRMS,
				DynamicRange:  profile.DynamicRange,
				Duration:      time.Duration(float64(time.Second) * profile.Duration),
				OptimalScale:  profile.OptimalBaseScale,
			})
		} else {
			// On error, just quit the program
			p.Quit()
		}
	}()

	// Run the Bubbletea UI
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error running UI: %v\n", err)
		os.Exit(1)
	}

	// Check for analysis errors
	if analysisErr != nil {
		fmt.Printf("Error analyzing audio: %v\n", analysisErr)
		os.Exit(1)
	}

	// ============================================================================
	// PASS 2: Render video with Bubbletea UI
	// ============================================================================

	// Create Bubbletea program for Pass 2
	pass2Model := ui.NewPass2Model()
	p2 := tea.NewProgram(pass2Model)

	// Open streaming reader for Pass 2
	reader, err := audio.NewStreamingReader(inputFile)
	if err != nil {
		fmt.Printf("Error opening audio stream: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()

	// Initialize encoder with both video and audio
	enc, err := encoder.New(encoder.Config{
		OutputPath: outputFile,
		Width:      config.Width,
		Height:     config.Height,
		Framerate:  config.FPS,
		AudioPath:  inputFile, // Enable Phase 2B audio processing
	})
	if err != nil {
		fmt.Printf("Error creating encoder: %v\n", err)
		os.Exit(1)
	}

	err = enc.Initialize()
	if err != nil {
		fmt.Printf("Error initializing encoder: %v\n", err)
		os.Exit(1)
	}

	// Run rendering in goroutine
	var encodingErr error
	var perfStats struct {
		fftTime, binTime, drawTime, writeTime, totalTime time.Duration
	}

	go func() {
		defer enc.Close()

		// Load background image
		bgImage, err := renderer.LoadBackgroundImage("assets/bg.png")
		if err != nil {
			bgImage = nil
		}

		// Load font for center text
		fontFace, err := renderer.LoadFont("assets/Poppins-Regular.ttf", 48)
		if err != nil {
			fontFace = nil
		}

		// Create audio processor and frame renderer
		processor := audio.NewProcessor()
		frame := renderer.NewFrame(bgImage, fontFace)

		// Calculate frames from profile
		numFrames := profile.NumFrames

		// Profiling variables
		var totalFFT, totalBin, totalDraw, totalWrite time.Duration
		renderStartTime := time.Now()

		// Get audio format information for codec display
		audioSampleRate := reader.SampleRate()
		audioChannels := reader.NumChannels()
		audioChannelStr := "mono"
		if audioChannels == 2 {
			audioChannelStr = "stereo"
		} else if audioChannels > 2 {
			audioChannelStr = fmt.Sprintf("%dch", audioChannels)
		}
		audioCodecInfo := fmt.Sprintf("AAC %.1fkHz %s", float64(audioSampleRate)/1000.0, audioChannelStr)

		// CAVA algorithm state
		prevBarHeights := make([]float64, config.NumBars)
		cavaPeaks := make([]float64, config.NumBars)
		cavaFall := make([]float64, config.NumBars)
		cavaMem := make([]float64, config.NumBars)

		// Pre-allocate reusable buffers to avoid allocations in render loop
		barHeights := make([]float64, config.NumBars)
		rearrangedHeights := make([]float64, config.NumBars)

		// Calculate gravity modifier (CAVA formula)
		gravityMod := math.Pow(60.0/config.Framerate, 2.5) * 1.54 / config.NoiseReduction
		if gravityMod < 1.0 {
			gravityMod = 1.0
		}

		// Auto-sensitivity adjustment (CAVA-style)
		sensitivity := 1.0

		// Sliding buffer for FFT: we read samplesPerFrame but need FFTSize for FFT
		samplesPerFrame := config.SampleRate / config.FPS
		fftBuffer := make([]float64, config.FFTSize)

		// Pre-fill buffer with first chunk
		initialChunk, err := reader.ReadChunk(config.FFTSize)
		if err != nil {
			encodingErr = fmt.Errorf("error reading initial audio chunk: %w", err)
			p2.Quit()
			return
		}
		copy(fftBuffer, initialChunk)

		for frameNum := 0; frameNum < numFrames; frameNum++ {
			// Use current buffer for FFT
			chunk := fftBuffer[:config.FFTSize]

			// Compute FFT
			t0 := time.Now()
			coeffs := processor.ProcessChunk(chunk)
			totalFFT += time.Since(t0)

			// Compute magnitudes and bin into bars using optimal baseScale from Pass 1
			t0 = time.Now()
			audio.BinFFT(coeffs, sensitivity, profile.OptimalBaseScale, barHeights)
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
			audio.RearrangeFrequenciesCenterOut(prevBarHeights, rearrangedHeights)

			// Generate frame image
			t0 = time.Now()
			frame.Draw(rearrangedHeights)
			totalDraw += time.Since(t0)

			// Encode frame directly
			t0 = time.Now()
			img := frame.GetImage()
			if err := enc.WriteFrameRGBA(img.Pix); err != nil {
				encodingErr = fmt.Errorf("error encoding frame %d: %w", frameNum, err)
				p2.Quit()
				return
			}
			totalWrite += time.Since(t0)

			// Send progress update every 3 frames
			// Send frame data for preview every 6 frames (5Hz at 30fps - good balance)
			var frameData *image.RGBA
			if frameNum%6 == 0 {
				frameData = img
			}

			if frameNum%3 == 0 {
				elapsed := time.Since(renderStartTime)

				// Copy bar heights for UI (use rearranged for better visual)
				barHeightsCopy := make([]float64, len(rearrangedHeights))
				copy(barHeightsCopy, rearrangedHeights)

				// Estimate file size (rough calculation: bitrate * duration)
				// Assuming ~4 Mbps video + ~192 kbps audio
				videoDuration := time.Duration(numFrames) * time.Second / time.Duration(config.FPS)
				estimatedBitrate := 4.0 * 1024 * 1024 / 8 // 4 Mbps in bytes/sec
				estimatedSize := int64(float64(estimatedBitrate) * videoDuration.Seconds() * float64(frameNum) / float64(numFrames))

				p2.Send(ui.Pass2Progress{
					Frame:       frameNum + 1,
					TotalFrames: numFrames,
					Elapsed:     elapsed,
					BarHeights:  barHeightsCopy,
					FileSize:    estimatedSize,
					Sensitivity: sensitivity,
					FrameData:   frameData, // Send frame every 6 frames for preview
					VideoCodec:  fmt.Sprintf("H.264 %d×%d", config.Width, config.Height),
					AudioCodec:  audioCodecInfo,
				})
			}

			// Advance sliding buffer for next frame
			// Read samplesPerFrame new samples and shift buffer
			if frameNum < numFrames-1 { // Don't read past end
				newSamples, err := reader.ReadChunk(samplesPerFrame)
				if err == io.EOF {
					break // Unexpected EOF
				}
				if err != nil {
					encodingErr = fmt.Errorf("error reading audio: %w", err)
					p2.Quit()
					return
				}

				// Shift buffer left by samplesPerFrame, append new samples
				copy(fftBuffer, fftBuffer[samplesPerFrame:])
				copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
			}
		}

		// Process audio through the encoder
		if err := enc.ProcessAudio(); err != nil {
			encodingErr = fmt.Errorf("error processing audio: %w", err)
			p2.Quit()
			return
		}

		// Finalize encoding
		if err := enc.Close(); err != nil {
			encodingErr = fmt.Errorf("error closing encoder: %w", err)
			p2.Quit()
			return
		}

		// Calculate total time
		totalTime := time.Since(renderStartTime)

		// Store performance stats
		perfStats.fftTime = totalFFT
		perfStats.binTime = totalBin
		perfStats.drawTime = totalDraw
		perfStats.writeTime = totalWrite
		perfStats.totalTime = totalTime

		// Get actual file size
		fileInfo, err := os.Stat(outputFile)
		var actualFileSize int64
		if err == nil {
			actualFileSize = fileInfo.Size()
		}

		// Calculate samples processed (sample rate * duration)
		samplesProcessed := int64(profile.SampleRate) * int64(profile.Duration)

		// Send completion message
		p2.Send(ui.Pass2Complete{
			OutputFile:       outputFile,
			Duration:         totalTime,
			FileSize:         actualFileSize,
			TotalFrames:      numFrames,
			FFTTime:          totalFFT,
			BinTime:          totalBin,
			DrawTime:         totalDraw,
			EncodeTime:       totalWrite,
			TotalTime:        totalTime,
			SamplesProcessed: samplesProcessed,
		})
	}()

	// Run the Bubbletea UI
	if _, err := p2.Run(); err != nil {
		fmt.Printf("Error running UI: %v\n", err)
		os.Exit(1)
	}

	// Check for encoding errors
	if encodingErr != nil {
		fmt.Printf("Error during encoding: %v\n", encodingErr)
		os.Exit(1)
	}
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
	barHeights := make([]float64, config.NumBars)
	audio.BinFFT(coeffs, 1.0, baseScale, barHeights)

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
	rearrangedHeights := make([]float64, config.NumBars)
	audio.RearrangeFrequenciesCenterOut(barHeights, rearrangedHeights)

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
