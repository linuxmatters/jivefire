package audio

import (
	"fmt"
	"io"
	"math"
	"time"

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
	GlobalPeak   float64 // Highest peak magnitude across all frames
	GlobalRMS    float64 // Average RMS across all frames
	DynamicRange float64 // Ratio of GlobalPeak to GlobalRMS

	// Calculated optimal parameters
	OptimalBaseScale float64 // Replaces hardcoded 0.0075

	// Audio metadata
	SampleRate int
	Duration   float64 // Seconds
}

// ProgressCallback is called with progress updates during analysis
type ProgressCallback func(frame, totalFrames int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration)

// AnalyzeAudio performs Pass 1: stream through audio and collect statistics
func AnalyzeAudio(filename string, progressCb ProgressCallback) (*AudioProfile, error) {

	// Open streaming reader
	reader, err := NewStreamingReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio: %w", err)
	}
	defer reader.Close()

	profile := &AudioProfile{
		NumFrames:  0,                        // Will be set after we count actual samples
		Frames:     make([]FrameAnalysis, 0), // Will grow as we read
		SampleRate: reader.SampleRate(),
		Duration:   0, // Will be calculated from actual sample count
	}

	// Calculate frame size
	samplesPerFrame := config.SampleRate / config.FPS

	// Create FFT processor
	processor := NewProcessor()

	// Analyze each frame
	var sumRMS float64
	var maxPeak float64

	// Sliding buffer for FFT: we advance by samplesPerFrame but need FFTSize for FFT
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
			return nil, fmt.Errorf("error reading initial chunk: %w", err)
		}
		initialSamples = append(initialSamples, chunk...)
	}

	if len(initialSamples) == 0 {
		return nil, fmt.Errorf("no audio data in file")
	}

	copy(fftBuffer, initialSamples)

	startTime := time.Now()
	frameNum := 0

	for {
		// Use current buffer for FFT (copy to ensure we have full FFTSize)
		chunk := make([]float64, config.FFTSize)
		copy(chunk, fftBuffer)

		// Compute FFT
		coeffs := processor.ProcessChunk(chunk)

		// Analyze frequency bins
		analysis := analyzeFrame(coeffs, chunk)
		profile.Frames = append(profile.Frames, analysis)

		// Track global statistics
		if analysis.PeakMagnitude > maxPeak {
			maxPeak = analysis.PeakMagnitude
		}
		sumRMS += analysis.RMSLevel

		frameNum++

		// Send progress update via callback (throttle to every 3 frames for performance)
		if progressCb != nil && frameNum%3 == 0 {
			// Convert bar magnitudes to slice for progress update
			barHeights := make([]float64, config.NumBars)
			for i := 0; i < config.NumBars; i++ {
				barHeights[i] = analysis.BarMagnitudes[i]
			}

			elapsed := time.Since(startTime)
			// No total frames estimate available during first pass
			progressCb(frameNum, 0, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
		}

		// Advance sliding buffer for next frame
		// Read samplesPerFrame new samples and shift buffer
		// Keep reading until we get the requested number of samples or EOF
		newSamples := make([]float64, 0, samplesPerFrame)
		for len(newSamples) < samplesPerFrame {
			chunk, err := reader.ReadChunk(samplesPerFrame - len(newSamples))
			if err != nil {
				if err == io.EOF {
					// If we got some samples, use them; otherwise we're done
					if len(newSamples) == 0 {
						// Send final progress update
						if progressCb != nil {
							barHeights := make([]float64, config.NumBars)
							for i := 0; i < config.NumBars; i++ {
								barHeights[i] = analysis.BarMagnitudes[i]
							}
							elapsed := time.Since(startTime)
							progressCb(frameNum, frameNum, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
						}
						break // Finished reading all audio
					}
					// Got partial frame at end of file, use what we have
					break
				}
				return nil, fmt.Errorf("error reading audio at frame %d: %w", frameNum, err)
			}
			newSamples = append(newSamples, chunk...)
		}

		// If we got no new samples, we're done
		if len(newSamples) == 0 {
			break
		}

		// Shift buffer left by samplesPerFrame, append new samples
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
	}

	// Set actual frame count and duration
	// Duration is based on the number of frames, not total samples read
	// Each frame represents samplesPerFrame of actual audio advancement
	profile.NumFrames = frameNum
	profile.Duration = float64(frameNum*samplesPerFrame) / float64(reader.SampleRate())

	// Calculate global statistics
	profile.GlobalPeak = maxPeak
	profile.GlobalRMS = sumRMS / float64(profile.NumFrames)

	// Avoid division by zero
	if profile.GlobalRMS > 0 {
		profile.DynamicRange = profile.GlobalPeak / profile.GlobalRMS
	} else {
		profile.DynamicRange = 0
	}

	// Calculate optimal baseScale
	// Goal: GlobalPeak should map to ~0.8-0.9 in normalized space
	// Current formula: scaled = magnitude * baseScale * sensitivity
	// We want: GlobalPeak * baseScale * 1.0 ≈ 0.85
	if profile.GlobalPeak > 0 {
		profile.OptimalBaseScale = 0.85 / profile.GlobalPeak
	} else {
		// Fallback to original hardcoded value if no audio detected
		profile.OptimalBaseScale = 0.0075
	}

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
