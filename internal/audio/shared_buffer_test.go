package audio

import (
	"sync"
	"testing"
	"time"
)

func TestSharedAudioBuffer_BasicWriteRead(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	// Write some samples
	samples := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	if err := buf.Write(samples); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Verify availability
	if got := buf.AvailableForFFT(); got != 5 {
		t.Errorf("AvailableForFFT = %d, want 5", got)
	}
	if got := buf.AvailableForEncoder(); got != 5 {
		t.Errorf("AvailableForEncoder = %d, want 5", got)
	}

	// Read for FFT
	fftSamples, err := buf.ReadForFFT(3)
	if err != nil {
		t.Fatalf("ReadForFFT failed: %v", err)
	}
	if len(fftSamples) != 3 {
		t.Errorf("ReadForFFT returned %d samples, want 3", len(fftSamples))
	}
	if fftSamples[0] != 0.1 || fftSamples[2] != 0.3 {
		t.Errorf("ReadForFFT returned wrong values: %v", fftSamples)
	}

	// FFT position advanced, encoder position unchanged
	if got := buf.AvailableForFFT(); got != 2 {
		t.Errorf("AvailableForFFT after read = %d, want 2", got)
	}
	if got := buf.AvailableForEncoder(); got != 5 {
		t.Errorf("AvailableForEncoder after FFT read = %d, want 5 (unchanged)", got)
	}
}

func TestSharedAudioBuffer_IndependentReaders(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	samples := []float64{1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0}
	if err := buf.Write(samples); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// Read 4 samples for FFT
	fft1, _ := buf.ReadForFFT(4)
	if len(fft1) != 4 || fft1[0] != 1.0 {
		t.Errorf("FFT read 1 wrong: %v", fft1)
	}

	// Read 2 samples for encoder (non-blocking)
	enc1, _ := buf.ReadForEncoderNonBlocking(2)
	if len(enc1) != 2 || enc1[0] != 1.0 {
		t.Errorf("Encoder read 1 wrong: %v", enc1)
	}

	// Read more for FFT
	fft2, _ := buf.ReadForFFT(4)
	if len(fft2) != 4 || fft2[0] != 5.0 {
		t.Errorf("FFT read 2 wrong: %v", fft2)
	}

	// Read more for encoder
	enc2, _ := buf.ReadForEncoderNonBlocking(4)
	if len(enc2) != 4 || enc2[0] != 3.0 {
		t.Errorf("Encoder read 2 wrong: %v", enc2)
	}

	// Both have read different amounts
	if got := buf.AvailableForFFT(); got != 0 {
		t.Errorf("AvailableForFFT = %d, want 0", got)
	}
	if got := buf.AvailableForEncoder(); got != 2 {
		t.Errorf("AvailableForEncoder = %d, want 2", got)
	}
}

func TestSharedAudioBuffer_EncoderBlocking(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	// Start a goroutine that will write samples after a delay
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		buf.Write([]float64{1.0, 2.0, 3.0, 4.0})
	}()

	// This should block until samples are available
	start := time.Now()
	samples, err := buf.ReadForEncoder(4)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ReadForEncoder failed: %v", err)
	}
	if len(samples) != 4 {
		t.Errorf("Got %d samples, want 4", len(samples))
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("ReadForEncoder didn't block: elapsed %v", elapsed)
	}

	wg.Wait()
}

func TestSharedAudioBuffer_CloseUnblocksReaders(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	// Write partial data
	buf.Write([]float64{1.0, 2.0})

	// Start a goroutine that will close after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		buf.Close()
	}()

	// Request more than available - should block then return partial
	start := time.Now()
	samples, err := buf.ReadForEncoder(10)
	elapsed := time.Since(start)

	// Should get partial samples, no error
	if err != nil {
		t.Fatalf("ReadForEncoder returned unexpected error: %v", err)
	}
	if len(samples) != 2 {
		t.Errorf("Got %d samples, want 2 (partial)", len(samples))
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("ReadForEncoder didn't block: elapsed %v", elapsed)
	}

	// Next read should return ErrBufferClosed
	_, err = buf.ReadForEncoder(1)
	if err != ErrBufferClosed {
		t.Errorf("Expected ErrBufferClosed, got %v", err)
	}
}

func TestSharedAudioBuffer_NonBlockingReturnsNil(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	// Request more than available (non-blocking)
	samples, err := buf.ReadForEncoderNonBlocking(10)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if samples != nil {
		t.Errorf("Expected nil, got %v", samples)
	}

	// Write some but not enough
	buf.Write([]float64{1.0, 2.0})

	samples, err = buf.ReadForEncoderNonBlocking(10)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if samples != nil {
		t.Errorf("Expected nil, got %v", samples)
	}

	// Now write enough
	buf.Write([]float64{3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0})

	samples, err = buf.ReadForEncoderNonBlocking(10)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if len(samples) != 10 {
		t.Errorf("Got %d samples, want 10", len(samples))
	}
}

func TestSharedAudioBuffer_Compact(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	// Write samples
	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = float64(i)
	}
	buf.Write(samples)

	// Both consumers read 50 samples
	buf.ReadForFFT(50)
	buf.ReadForEncoderNonBlocking(50)

	// Compact should remove the first 50
	buf.CompactBuffer()

	// Total should be 50 now
	if got := buf.TotalSamples(); got != 50 {
		t.Errorf("TotalSamples after compact = %d, want 50", got)
	}

	// Both should still have 50 available
	if got := buf.AvailableForFFT(); got != 50 {
		t.Errorf("AvailableForFFT after compact = %d, want 50", got)
	}
	if got := buf.AvailableForEncoder(); got != 50 {
		t.Errorf("AvailableForEncoder after compact = %d, want 50", got)
	}

	// Next read should get correct values (50-99)
	fftSamples, _ := buf.ReadForFFT(5)
	if fftSamples[0] != 50.0 {
		t.Errorf("After compact, first FFT sample = %v, want 50.0", fftSamples[0])
	}
}

func TestSharedAudioBuffer_CompactPartial(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	samples := make([]float64, 100)
	for i := range samples {
		samples[i] = float64(i)
	}
	buf.Write(samples)

	// FFT reads 70, encoder reads 30
	buf.ReadForFFT(70)
	buf.ReadForEncoderNonBlocking(30)

	// Compact should only remove 30 (the minimum)
	buf.CompactBuffer()

	if got := buf.TotalSamples(); got != 70 {
		t.Errorf("TotalSamples after partial compact = %d, want 70", got)
	}

	// FFT should have 30 remaining (was 100-70=30, now adjusted)
	if got := buf.AvailableForFFT(); got != 30 {
		t.Errorf("AvailableForFFT after partial compact = %d, want 30", got)
	}
	// Encoder should have 70 remaining
	if got := buf.AvailableForEncoder(); got != 70 {
		t.Errorf("AvailableForEncoder after partial compact = %d, want 70", got)
	}
}

func TestSharedAudioBuffer_Reset(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)

	buf.Write([]float64{1.0, 2.0, 3.0})
	buf.ReadForFFT(2)
	buf.Close()

	// Reset
	buf.Reset()

	// Should be empty and open
	if buf.TotalSamples() != 0 {
		t.Errorf("TotalSamples after reset = %d, want 0", buf.TotalSamples())
	}
	if buf.IsClosed() {
		t.Error("Buffer should not be closed after reset")
	}
	if buf.AvailableForFFT() != 0 {
		t.Error("FFT position should be reset")
	}
	if buf.AvailableForEncoder() != 0 {
		t.Error("Encoder position should be reset")
	}

	// Should be able to write again
	if err := buf.Write([]float64{4.0, 5.0}); err != nil {
		t.Errorf("Write after reset failed: %v", err)
	}
}

func TestSharedAudioBuffer_WriteAfterClose(t *testing.T) {
	buf := NewSharedAudioBuffer(1024)
	buf.Close()

	err := buf.Write([]float64{1.0})
	if err != ErrBufferClosed {
		t.Errorf("Write after close should return ErrBufferClosed, got %v", err)
	}
}

func TestSharedAudioBuffer_ConcurrentAccess(t *testing.T) {
	buf := NewSharedAudioBuffer(0) // Use default capacity

	const numSamples = 100000
	const chunkSize = 1000

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numSamples; i += chunkSize {
			chunk := make([]float64, chunkSize)
			for j := 0; j < chunkSize; j++ {
				chunk[j] = float64(i + j)
			}
			if err := buf.Write(chunk); err != nil {
				t.Errorf("Write failed: %v", err)
				return
			}
		}
		buf.Close()
	}()

	// FFT reader goroutine
	fftTotal := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			samples, err := buf.ReadForFFT(512)
			if err == ErrBufferClosed {
				break
			}
			fftTotal += len(samples)
			time.Sleep(time.Microsecond) // Simulate some work
		}
	}()

	// Encoder reader goroutine
	encTotal := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			samples, err := buf.ReadForEncoder(1024)
			if err == ErrBufferClosed {
				break
			}
			encTotal += len(samples)
		}
	}()

	wg.Wait()

	if fftTotal != numSamples {
		t.Errorf("FFT read %d samples, want %d", fftTotal, numSamples)
	}
	if encTotal != numSamples {
		t.Errorf("Encoder read %d samples, want %d", encTotal, numSamples)
	}
}
