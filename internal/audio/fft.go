package audio

import (
	"math"

	"github.com/linuxmatters/jivefire/internal/config"
	"gonum.org/v1/gonum/dsp/fourier"
)

// ApplyHanning applies a Hanning window to the input data
func ApplyHanning(data []float64) []float64 {
	windowed := make([]float64, len(data))
	n := len(data)
	for i := range data {
		window := 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
		windowed[i] = data[i] * window
	}
	return windowed
}

// BinFFT bins FFT coefficients into bars and returns normalized values (0.0-1.0)
// CAVA-style approach: work in normalized space, apply maxBarHeight scaling later
// baseScale is calculated from Pass 1 analysis for optimal visualization
// result buffer is provided by caller to avoid allocations
func BinFFT(coeffs []complex128, sensitivity float64, baseScale float64, result []float64) {
	// Use only first half (positive frequencies)
	halfSize := len(coeffs) / 2

	// Use full spectrum up to Nyquist frequency (~22kHz at 44.1kHz sample rate)
	// This captures the complete audible range including high-frequency content
	// from cymbals, hi-hats, and musical "air" in stings and bumpers
	maxFreqBin := halfSize

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

		result[bar] = sum / float64(binsPerBar)
	}

	// CAVA-style processing: apply sensitivity, then normalize to 0.0-1.0 range
	// baseScale provided from Pass 1 analysis: OptimalBaseScale = 0.85 / GlobalPeak

	for i := range result {
		// Apply sensitivity to raw magnitude
		scaled := result[i] * baseScale * sensitivity

		// Noise gate on raw values (before log scale)
		if scaled < 0.01 {
			result[i] = 0
		} else {
			// Log scale for better visual distribution, normalize to ~0.0-1.0
			// Log10(1 + scaled*9) gives range [0, 1] for scaled in [0, 1]
			// We scale up for better dynamic range
			result[i] = math.Log10(1 + scaled*9)

			// DON'T clip here - let overshoot detection in main loop handle it
			// This allows sensitivity adjustment to detect actual overshoots
		}
	}
}

// RearrangeFrequenciesCenterOut creates a symmetric mirror pattern with most active frequencies at CENTER
// result buffer is provided by caller to avoid allocations
func RearrangeFrequenciesCenterOut(barHeights []float64, result []float64) {
	// Left side: frequencies 0→31 placed from CENTER → LEFT EDGE (most active at center)
	// Right side: frequencies 0→31 mirrored from CENTER → RIGHT EDGE
	// Result: Most active (bass) at center, less active (highs) at edges

	n := len(barHeights)
	center := n / 2

	// Place first half of frequencies mirrored from center outward
	for i := 0; i < n/2; i++ {
		// Left side: place from center going left (most active near center)
		result[center-1-i] = barHeights[i]
		// Right side: mirror (most active near center)
		result[center+i] = barHeights[i]
	}
}

// Processor handles FFT analysis for visualization
type Processor struct {
	fft *fourier.FFT
}

// NewProcessor creates a new audio processor
func NewProcessor() *Processor {
	return &Processor{
		fft: fourier.NewFFT(config.FFTSize),
	}
}

// ProcessChunk performs FFT on a chunk of audio samples
func (p *Processor) ProcessChunk(samples []float64) []complex128 {
	// Pad if needed
	chunk := samples
	if len(chunk) < config.FFTSize {
		padded := make([]float64, config.FFTSize)
		copy(padded, chunk)
		chunk = padded
	}

	// Apply Hanning window
	windowed := ApplyHanning(chunk)

	// Compute FFT
	return p.fft.Coefficients(nil, windowed)
}
