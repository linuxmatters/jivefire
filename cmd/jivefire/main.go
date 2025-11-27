package main

import (
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/linuxmatters/jivefire/internal/audio"
	"github.com/linuxmatters/jivefire/internal/cli"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/encoder"
	"github.com/linuxmatters/jivefire/internal/renderer"
	"github.com/linuxmatters/jivefire/internal/ui"
)

// version is set via ldflags at build time
// Local dev builds: "dev"
// Release builds: git tag (e.g. "v0.1.0")
var version = "dev"

var CLI struct {
	Input           string `arg:"" name:"input" help:"Input WAV file" optional:""`
	Output          string `arg:"" name:"output" help:"Output MP4 file" optional:""`
	Episode         int    `help:"Episode number" default:"0"`
	Title           string `help:"Podcast title" default:"Podcast Title"`
	Channels        int    `help:"Audio channels in MP4: 1 (mono) or 2 (stereo)" default:"1"`
	BarColor        string `help:"Bar color in hex format (e.g., #A40000 or A40000)"`
	TextColor       string `help:"Text color in hex format (e.g., #F8B31D or F8B31D)"`
	BackgroundImage string `help:"Path to custom background image (PNG, 1280x720)"`
	ThumbnailImage  string `help:"Path to custom thumbnail image (PNG, 1280x720)"`
	NoPreview       bool   `help:"Disable video preview during encoding"`
	Version         bool   `help:"Show version information"`
}

func main() {
	ctx := kong.Parse(&CLI,
		kong.Name("jivefire"),
		kong.Description("Spin your podcast .wav into a groovy MP4 visualiser."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.Help(cli.StyledHelpPrinter(kong.HelpOptions{Compact: true})),
	)

	// Handle version flag
	if CLI.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	// Validate required arguments when not showing version
	if CLI.Input == "" || CLI.Output == "" {
		cli.PrintError("<input> and <output> are required")
		os.Exit(1)
	}

	// Validate input file exists
	if _, err := os.Stat(CLI.Input); os.IsNotExist(err) {
		cli.PrintError(fmt.Sprintf("input file does not exist: %s", CLI.Input))
		os.Exit(1)
	}

	// Validate channels
	if CLI.Channels != 1 && CLI.Channels != 2 {
		cli.PrintError(fmt.Sprintf("invalid channels value: %d (must be 1 or 2)", CLI.Channels))
		os.Exit(1)
	}

	// Build runtime config from CLI arguments
	runtimeConfig := &config.RuntimeConfig{}

	// Parse and validate bar color if provided
	if CLI.BarColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.BarColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --bar-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.BarColorR = &r
		runtimeConfig.BarColorG = &g
		runtimeConfig.BarColorB = &b
	}

	// Parse and validate text color if provided
	if CLI.TextColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.TextColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --text-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.TextColorR = &r
		runtimeConfig.TextColorG = &g
		runtimeConfig.TextColorB = &b
	}

	// Validate background image if provided
	if CLI.BackgroundImage != "" {
		if _, err := os.Stat(CLI.BackgroundImage); os.IsNotExist(err) {
			cli.PrintError(fmt.Sprintf("background image does not exist: %s", CLI.BackgroundImage))
			os.Exit(1)
		}
		runtimeConfig.BackgroundImagePath = CLI.BackgroundImage
	}

	// Validate thumbnail image if provided
	if CLI.ThumbnailImage != "" {
		if _, err := os.Stat(CLI.ThumbnailImage); os.IsNotExist(err) {
			cli.PrintError(fmt.Sprintf("thumbnail image does not exist: %s", CLI.ThumbnailImage))
			os.Exit(1)
		}
		runtimeConfig.ThumbnailImagePath = CLI.ThumbnailImage
	}

	inputFile := CLI.Input
	outputFile := CLI.Output
	channels := CLI.Channels
	noPreview := CLI.NoPreview

	_ = ctx // Kong context available for future use

	// Generate video using 2-pass streaming approach
	generateVideo(inputFile, outputFile, channels, noPreview, runtimeConfig)
}

func generateVideo(inputFile string, outputFile string, channels int, noPreview bool, runtimeConfig *config.RuntimeConfig) {
	// Track overall timing from the very start
	overallStartTime := time.Now()

	thumbnailPath := strings.Replace(outputFile, ".mp4", ".png", 1)
	thumbnailStartTime := time.Now()
	if err := renderer.GenerateThumbnail(thumbnailPath, CLI.Title, runtimeConfig); err != nil {
		cli.PrintError(fmt.Sprintf("failed to generate thumbnail: %v", err))
		os.Exit(1)
	}
	thumbnailDuration := time.Since(thumbnailStartTime)

	// Create Bubbletea program for Pass 1
	model := ui.NewPass1Model()
	p := tea.NewProgram(model)

	// Run analysis in a goroutine and send progress updates
	var profile *audio.AudioProfile
	var analysisErr error
	var pass1Duration time.Duration

	pass1StartTime := time.Now()

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

		// Capture Pass 1 duration
		pass1Duration = time.Since(pass1StartTime)

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
		cli.PrintError(fmt.Sprintf("running UI: %v", err))
		os.Exit(1)
	}

	// Check for analysis errors
	if analysisErr != nil {
		cli.PrintError(fmt.Sprintf("analyzing audio: %v", analysisErr))
		os.Exit(1)
	}

	// Create Bubbletea program for Pass 2
	pass2Model := ui.NewPass2Model(noPreview)
	p2 := tea.NewProgram(pass2Model)

	// Open streaming reader for Pass 2
	reader, err := audio.NewStreamingReader(inputFile)
	if err != nil {
		cli.PrintError(fmt.Sprintf("opening audio stream: %v", err))
		os.Exit(1)
	}
	defer reader.Close()

	// Initialize encoder with video and audio (using new sample-based API)
	enc, err := encoder.New(encoder.Config{
		OutputPath:    outputFile,
		Width:         config.Width,
		Height:        config.Height,
		Framerate:     config.FPS,
		SampleRate:    reader.SampleRate(), // Use sample rate from audio file
		AudioChannels: channels,            // Mono (1) or stereo (2)
	})
	if err != nil {
		cli.PrintError(fmt.Sprintf("creating encoder: %v", err))
		os.Exit(1)
	}

	err = enc.Initialize()
	if err != nil {
		cli.PrintError(fmt.Sprintf("initializing encoder: %v", err))
		os.Exit(1)
	}

	// Run rendering in goroutine
	var encodingErr error
	var perfStats struct {
		fftTime, binTime, drawTime, writeTime, totalTime time.Duration
	}

	go func() {
		defer enc.Close()

		// Load background image (custom or embedded)
		bgImage, err := renderer.LoadBackgroundImage(runtimeConfig)
		if err != nil {
			bgImage = nil
		}

		// Load font for center text (embedded)
		fontFace, err := renderer.LoadFont(48)
		if err != nil {
			fontFace = nil
		}

		// Create audio processor and frame renderer
		processor := audio.NewProcessor()
		frame := renderer.NewFrame(bgImage, fontFace, CLI.Episode, CLI.Title, runtimeConfig)

		// Calculate frames from profile
		numFrames := profile.NumFrames

		// Profiling variables
		var totalFFT, totalBin, totalDraw, totalWrite time.Duration
		renderStartTime := time.Now()

		// Get audio format information for codec display
		audioSampleRate := reader.SampleRate()
		// Use output channel count (from CLI), not input channel count
		outputChannels := channels
		audioChannelStr := "mono"
		if outputChannels == 2 {
			audioChannelStr = "stereo"
		} else if outputChannels > 2 {
			audioChannelStr = fmt.Sprintf("%dch", outputChannels)
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
		// Keep reading until we get the requested number of samples or EOF
		var initialSamples []float64
		for len(initialSamples) < config.FFTSize {
			chunk, err := reader.ReadChunk(config.FFTSize - len(initialSamples))
			if err != nil {
				if err == io.EOF {
					break // Use what we have
				}
				encodingErr = fmt.Errorf("error reading initial audio chunk: %w", err)
				p2.Quit()
				return
			}
			initialSamples = append(initialSamples, chunk...)
		}

		if len(initialSamples) == 0 {
			encodingErr = fmt.Errorf("no audio data available")
			p2.Quit()
			return
		}

		copy(fftBuffer, initialSamples)

		// Write initial audio samples to encoder (first samplesPerFrame worth)
		// This corresponds to the audio for frame 0
		initialAudioSamples := make([]float32, samplesPerFrame)
		for i := 0; i < samplesPerFrame && i < len(initialSamples); i++ {
			initialAudioSamples[i] = float32(initialSamples[i])
		}
		if err := enc.WriteAudioSamples(initialAudioSamples); err != nil {
			encodingErr = fmt.Errorf("error writing initial audio: %w", err)
			p2.Quit()
			return
		}

		// Process frames until we run out of audio
		frameNum := 0
		for frameNum < numFrames {
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
			// Skip frame data entirely if preview is disabled for better batch performance
			var frameData *image.RGBA
			if !noPreview && frameNum%6 == 0 {
				frameData = img
			}

			if frameNum%3 == 0 {
				elapsed := time.Since(renderStartTime)

				// Copy bar heights for UI (use rearranged for better visual)
				barHeightsCopy := make([]float64, len(rearrangedHeights))
				copy(barHeightsCopy, rearrangedHeights)

				// Get actual file size from disk (not an estimate)
				var currentFileSize int64
				if fileInfo, err := os.Stat(outputFile); err == nil {
					currentFileSize = fileInfo.Size()
				}

				p2.Send(ui.Pass2Progress{
					Frame:       frameNum + 1,
					TotalFrames: numFrames,
					Elapsed:     elapsed,
					BarHeights:  barHeightsCopy,
					FileSize:    currentFileSize,
					Sensitivity: sensitivity,
					FrameData:   frameData, // Send frame every 6 frames for preview
					VideoCodec:  fmt.Sprintf("H.264 %d×%d", config.Width, config.Height),
					AudioCodec:  audioCodecInfo,
				})
			}

			// Advance to next frame
			frameNum++

			// Advance sliding buffer for next frame
			// Keep reading until we get the requested number of samples or EOF
			newSamples := make([]float64, 0, samplesPerFrame)
			for len(newSamples) < samplesPerFrame {
				chunk, err := reader.ReadChunk(samplesPerFrame - len(newSamples))
				if err != nil {
					if err == io.EOF {
						// If we got no new samples at all, we're done
						if len(newSamples) == 0 {
							// Break out of the frame loop - no more audio
							frameNum = numFrames
							break
						}
						// Got partial frame at end of file, use what we have
						break
					}
					encodingErr = fmt.Errorf("error reading audio: %w", err)
					p2.Quit()
					return
				}
				newSamples = append(newSamples, chunk...)
			}

			// If we got no new samples, we're done
			if len(newSamples) == 0 {
				break
			}

			// Write audio samples for this frame to encoder
			// Convert float64 samples to float32 for AAC encoder
			audioSamples := make([]float32, len(newSamples))
			for i, s := range newSamples {
				audioSamples[i] = float32(s)
			}
			if err := enc.WriteAudioSamples(audioSamples); err != nil {
				encodingErr = fmt.Errorf("error writing audio at frame %d: %w", frameNum, err)
				p2.Quit()
				return
			}

			// Shift buffer left by samplesPerFrame, append new samples
			copy(fftBuffer, fftBuffer[samplesPerFrame:])
			// Pad with zeros if we got fewer samples than expected
			if len(newSamples) < samplesPerFrame {
				copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
				// Zero-fill the remaining space
				for i := config.FFTSize - samplesPerFrame + len(newSamples); i < config.FFTSize; i++ {
					fftBuffer[i] = 0
				}
			} else {
				copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
			}
		}

		// Flush any remaining audio after all video frames are written
		// This encodes any samples remaining in the FIFO and flushes the encoder
		if err := enc.FlushAudioEncoder(); err != nil {
			encodingErr = fmt.Errorf("error flushing audio: %w", err)
			p2.Quit()
			return
		}

		// Finalize encoding
		if err := enc.Close(); err != nil {
			encodingErr = fmt.Errorf("error closing encoder: %w", err)
			p2.Quit()
			return
		}

		// Calculate total time (from Pass 2 render start, not including Pass 1)
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

		// Calculate overall total time from the very beginning
		overallTotalTime := time.Since(overallStartTime)

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
			TotalTime:        overallTotalTime, // Use overall total, not just Pass 2
			Pass1Time:        pass1Duration,
			ThumbnailTime:    thumbnailDuration,
			SamplesProcessed: samplesProcessed,
		})
	}()

	// Run the Bubbletea UI
	if _, err := p2.Run(); err != nil {
		cli.PrintError(fmt.Sprintf("running UI: %v", err))
		os.Exit(1)
	}

	// Check for encoding errors
	if encodingErr != nil {
		cli.PrintError(fmt.Sprintf("during encoding: %v", encodingErr))
		os.Exit(1)
	}
}
