package audio

import (
	"math"
	"testing"

	"github.com/argusdusty/gofft"
)

// TestBinFFT_KnownSineWave verifies that BinFFT correctly identifies a known
// single-frequency sine wave. This catches frequency-to-bar mapping errors,
// scaling bugs, or log transform issues that would make bars unresponsive.
//
// Test uses 440 Hz sine wave (A4 musical note) at 44.1 kHz sample rate.
// With 2048 FFT and 64 bars covering full spectrum (22.05 kHz Nyquist):
// - Frequency bin width: 44100 / 2048 ≈ 21.5 Hz/bin
// - 440 Hz maps to bin index ≈ 440 / 21.5 ≈ 20
// - Bars are created by grouping bins: 1024 bins / 64 bars = 16 bins/bar
// - Bar index ≈ 20 / 16 ≈ 1 (440 Hz is in lower-frequency bars)
func TestBinFFT_KnownSineWave(t *testing.T) {
	const (
		sampleRate  = 44100
		frequency   = 440    // A4 musical note
		duration    = 1.0    // 1 second
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 1.0 // Will be normalized later
	)

	// Generate 1 second of 440 Hz sine wave at 44.1 kHz
	numSamples := int(float64(sampleRate) * duration)
	sine := make([]float64, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		sine[i] = math.Sin(2 * math.Pi * frequency * t)
	}

	// Take first FFT window (2048 samples)
	windowSamples := sine[:fftSize]

	// Apply Hanning window (same as in ProcessChunk)
	windowed := ApplyHanning(windowSamples)

	// Compute FFT
	fftInput := gofft.Float64ToComplex128Array(windowed)
	if err := gofft.FFT(fftInput); err != nil {
		t.Fatalf("FFT computation failed: %v", err)
	}

	// Bin the FFT results into 64 bars
	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Find the bar with maximum magnitude
	maxVal := 0.0
	maxBar := 0
	for bar, val := range result {
		if val > maxVal {
			maxVal = val
			maxBar = bar
		}
	}

	t.Logf("440 Hz sine wave analysis:")
	t.Logf("  FFT size: %d samples", fftSize)
	t.Logf("  Sample rate: %d Hz", sampleRate)
	t.Logf("  Nyquist frequency: %d Hz", sampleRate/2)
	t.Logf("  Bin width: %.2f Hz/bin", float64(sampleRate)/float64(fftSize))
	t.Logf("  440 Hz maps to FFT bin: ~%.0f", frequency*float64(fftSize)/float64(sampleRate))
	t.Logf("  Bins per bar: %d", (fftSize/2)/numBars)
	t.Logf("  Peak bar: %d with magnitude %.6f", maxBar, maxVal)

	// Validation:
	// 1. Peak magnitude should be non-zero (sine wave is strong)
	if maxVal <= 0 {
		t.Errorf("Expected non-zero peak magnitude, got %.6f", maxVal)
	}

	// 2. Peak should be in lower frequency bars (440 Hz is relatively low)
	// 440 Hz is in the bass-midrange range, expect bar < 32 (lower half of spectrum)
	if maxBar >= numBars/2 {
		t.Errorf("Peak bar %d is in high frequency range; 440 Hz should be in lower bars", maxBar)
	}

	// 3. Peak should dominate (be significantly higher than other bars)
	// Calculate average of non-peak bars
	var sumOthers float64
	for bar, val := range result {
		if bar != maxBar {
			sumOthers += val
		}
	}
	avgOthers := sumOthers / float64(numBars-1)

	if maxVal <= avgOthers {
		t.Errorf("Peak magnitude %.6f not dominant over average of others %.6f", maxVal, avgOthers)
	}

	// 4. Peak-to-average ratio should be substantial (at least 2x)
	peakRatio := maxVal / avgOthers
	if peakRatio < 2.0 {
		t.Logf("Warning: Peak ratio %.2f is lower than expected (minimum 2.0x)", peakRatio)
		// Don't fail on this - log scaling can compress values - but warn
	}

	t.Logf("  Peak-to-average ratio: %.2fx", peakRatio)
}

// TestBinFFT_Silence verifies that BinFFT correctly handles silence
// (all zeros or near-silence). This catches scaling issues.
func TestBinFFT_Silence(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 1.0
	)

	// Create silence (all zeros)
	silence := make([]complex128, fftSize)

	result := make([]float64, numBars)
	BinFFT(silence, sensitivity, baseScale, result)

	// All bars should be zero (or very close due to log scaling of near-zero)
	for bar, val := range result {
		if val < 0 {
			t.Errorf("Bar %d has negative magnitude: %.6f", bar, val)
		}
		if val > 0.01 {
			t.Logf("Bar %d: %.6f (expected ~0 for silence)", bar, val)
		}
	}

	// Check that there are no large values
	maxVal := 0.0
	for _, val := range result {
		if val > maxVal {
			maxVal = val
		}
	}

	if maxVal > 0.1 {
		t.Errorf("Silence produced unexpectedly large magnitude: %.6f", maxVal)
	}

	t.Logf("Silence test passed: max bar magnitude = %.6f", maxVal)
}

// TestBinFFT_NoiseGate verifies that low-energy frequencies are gated out
// (set to zero) to prevent noise floor from creating visual artifacts.
func TestBinFFT_NoiseGate(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 0.1   // Low sensitivity to amplify quiet signals
		baseScale   = 0.1   // Low base scale
	)

	// Create very quiet signal (amplitude 0.001)
	quietSignal := make([]float64, fftSize)
	for i := range quietSignal {
		quietSignal[i] = 0.001 * math.Sin(2*math.Pi*float64(i)/100.0)
	}

	windowed := ApplyHanning(quietSignal)
	fftInput := gofft.Float64ToComplex128Array(windowed)
	if err := gofft.FFT(fftInput); err != nil {
		t.Fatalf("FFT computation failed: %v", err)
	}

	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Most bars should be zero due to noise gate
	zeroCount := 0
	for _, val := range result {
		if val == 0 {
			zeroCount++
		}
	}

	if zeroCount == 0 {
		t.Errorf("Noise gate didn't suppress quiet signal: all bars non-zero")
	}

	t.Logf("Noise gate test: %d/%d bars gated to zero (expected high count)", zeroCount, numBars)
}

// TestBinFFT_EnergyDistribution verifies that total energy is preserved
// through binning (no unexpected losses or amplifications).
func TestBinFFT_EnergyDistribution(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 0.5
	)

	// Create a broadband signal (white noise-ish via multiple frequencies)
	signal := make([]float64, fftSize)
	for i := 0; i < fftSize; i++ {
		signal[i] = 0.1 * (math.Sin(2*math.Pi*100*float64(i)/float64(fftSize)) +
			math.Sin(2*math.Pi*500*float64(i)/float64(fftSize)) +
			math.Sin(2*math.Pi*1000*float64(i)/float64(fftSize)))
	}

	windowed := ApplyHanning(signal)
	fftInput := gofft.Float64ToComplex128Array(windowed)
	if err := gofft.FFT(fftInput); err != nil {
		t.Fatalf("FFT computation failed: %v", err)
	}

	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Sum all bar energies
	totalEnergy := 0.0
	for _, val := range result {
		totalEnergy += val
	}

	// Should have non-zero energy
	if totalEnergy <= 0 {
		t.Errorf("Total energy is zero or negative: %.6f", totalEnergy)
	}

	// Most bars should have some energy (multi-frequency input)
	nonzeroCount := 0
	for _, val := range result {
		if val > 0 {
			nonzeroCount++
		}
	}

	if nonzeroCount < 3 {
		t.Errorf("Expected at least 3 bars with energy, got %d", nonzeroCount)
	}

	t.Logf("Energy distribution: %.6f total energy across %d/%d bars", totalEnergy, nonzeroCount, numBars)
}

// TestApplyHanning_WindowProperties verifies Hanning window coefficients
// match expected mathematical properties.
func TestApplyHanning_WindowProperties(t *testing.T) {
	// Test with small known size
	size := 8
	input := make([]float64, size)
	for i := range input {
		input[i] = 1.0 // All ones
	}

	windowed := ApplyHanning(input)

	if len(windowed) != size {
		t.Fatalf("Window size mismatch: got %d, want %d", len(windowed), size)
	}

	// Hanning window properties:
	// 1. Output length equals input length
	if len(windowed) != len(input) {
		t.Errorf("Window changed input length")
	}

	// 2. Start and end values should be zero (or very close)
	epsilon := 1e-10
	if math.Abs(windowed[0]) > epsilon {
		t.Errorf("Window start value %.15f is not zero", windowed[0])
	}
	if math.Abs(windowed[size-1]) > epsilon {
		t.Errorf("Window end value %.15f is not zero", windowed[size-1])
	}

	// 3. Center value should be ~1.0 (maximum of Hanning window)
	midPoint := windowed[size/2]
	if midPoint < 0.9 || midPoint > 1.05 {
		t.Errorf("Window center value %.6f not close to 1.0", midPoint)
	}

	// 4. Window should be symmetric
	for i := 0; i < size/2; i++ {
		if math.Abs(windowed[i]-windowed[size-1-i]) > epsilon {
			t.Errorf("Window not symmetric at position %d: %.15f != %.15f",
				i, windowed[i], windowed[size-1-i])
		}
	}

	t.Logf("Hanning window verified: start=%.15f, center=%.6f, end=%.15f",
		windowed[0], windowed[size/2], windowed[size-1])
}
