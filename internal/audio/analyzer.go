package audio

import (
	"fmt"
	"io"
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
	GlobalPeak   float64 // Highest peak magnitude across all frames
	GlobalRMS    float64 // Average RMS across all frames
	DynamicRange float64 // Ratio of GlobalPeak to GlobalRMS

	// Calculated optimal parameters
	OptimalBaseScale float64 // Replaces hardcoded 0.0075

	// Audio metadata
	SampleRate int
	Duration   float64 // Seconds
}

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
		if err == io.EOF {
			break // End of audio
		}
		if err != nil {
			return nil, fmt.Errorf("error reading chunk at frame %d: %w", frameNum, err)
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
