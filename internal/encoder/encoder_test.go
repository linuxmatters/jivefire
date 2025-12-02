package encoder

import (
	"os"
	"testing"
)

// TestEncoderPOC is a proof-of-concept test that encodes a single black frame
func TestEncoderPOC(t *testing.T) {
	outputPath := "../../testdata/poc-video.mp4"
	// Keep the file for inspection - comment out cleanup
	// defer os.Remove(outputPath)

	config := Config{
		OutputPath: outputPath,
		Width:      1280,
		Height:     720,
		Framerate:  30,
		HWAccel:    HWAccelNone, // Force software encoding for WriteFrame (RGB24→YUV420P)
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Initialize()
	if err != nil {
		t.Fatalf("Failed to initialize encoder: %v", err)
	}
	defer enc.Close()

	// Create a single black frame (RGB24 format)
	frameSize := config.Width * config.Height * 3 // RGB24 = 3 bytes per pixel
	blackFrame := make([]byte, frameSize)
	// blackFrame is already all zeros (black)

	// Write one frame
	err = enc.WriteFrame(blackFrame)
	if err != nil {
		t.Fatalf("Failed to write frame: %v", err)
	}

	// Close encoder (writes trailer)
	err = enc.Close()
	if err != nil {
		t.Fatalf("Failed to close encoder: %v", err)
	}

	// Verify output file exists and has non-zero size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}

	t.Logf("Successfully created video: %s (%d bytes)", outputPath, info.Size())
}

// TestEncoderRGBA tests the RGBA frame writing path
func TestEncoderRGBA(t *testing.T) {
	outputPath := "../../testdata/poc-rgba-video.mp4"
	defer os.Remove(outputPath) // Clean up after test

	config := Config{
		OutputPath: outputPath,
		Width:      1280,
		Height:     720,
		Framerate:  30,
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Initialize()
	if err != nil {
		t.Fatalf("Failed to initialize encoder: %v", err)
	}
	defer enc.Close()

	// Create a single frame with red color (RGBA format)
	frameSize := config.Width * config.Height * 4 // RGBA = 4 bytes per pixel
	redFrame := make([]byte, frameSize)
	for i := 0; i < frameSize; i += 4 {
		redFrame[i] = 255   // R = 255 (red)
		redFrame[i+1] = 0   // G = 0
		redFrame[i+2] = 0   // B = 0
		redFrame[i+3] = 255 // A = 255 (opaque)
	}

	// Write one frame using RGBA path
	err = enc.WriteFrameRGBA(redFrame)
	if err != nil {
		t.Fatalf("Failed to write RGBA frame: %v", err)
	}

	// Close encoder (writes trailer)
	err = enc.Close()
	if err != nil {
		t.Fatalf("Failed to close encoder: %v", err)
	}

	// Verify output file exists and has non-zero size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}

	t.Logf("Successfully created RGBA video: %s (%d bytes)", outputPath, info.Size())
}

// TestAudioFIFO_PopMoreThanAvailable verifies that Pop() returns nil when
// requesting more samples than available, catching potential slice bounds
// panics or incorrect partial returns.
func TestAudioFIFO_PopMoreThanAvailable(t *testing.T) {
	fifo := NewAudioFIFO(1024)

	// Push some samples
	samples := []float32{1.0, 2.0, 3.0}
	fifo.Push(samples)

	if fifo.Available() != 3 {
		t.Errorf("Available() = %d, want 3", fifo.Available())
	}

	// Request more than available
	result := fifo.Pop(10)
	if result != nil {
		t.Errorf("Pop(10) with 3 samples available returned %v, want nil", result)
	}

	// FIFO should be unchanged
	if fifo.Available() != 3 {
		t.Errorf("After failed Pop, Available() = %d, want 3", fifo.Available())
	}

	// Should still be able to pop exactly available amount
	result = fifo.Pop(3)
	if result == nil {
		t.Fatalf("Pop(3) with 3 samples available returned nil")
	}
	if len(result) != 3 {
		t.Errorf("Pop(3) returned %d samples, want 3", len(result))
	}
	if result[0] != 1.0 || result[1] != 2.0 || result[2] != 3.0 {
		t.Errorf("Pop(3) returned wrong values: %v", result)
	}

	// FIFO should be empty
	if fifo.Available() != 0 {
		t.Errorf("After Pop(3), Available() = %d, want 0", fifo.Available())
	}

	t.Logf("PopMoreThanAvailable test passed")
}

// TestAudioFIFO_EmptyBuffer verifies that operations on an empty FIFO
// don't panic and return appropriate values.
func TestAudioFIFO_EmptyBuffer(t *testing.T) {
	fifo := NewAudioFIFO(1024)

	// Test Available on empty buffer
	if fifo.Available() != 0 {
		t.Errorf("Empty FIFO Available() = %d, want 0", fifo.Available())
	}

	// Test Pop(0) on empty buffer - returns empty slice (not nil)
	result := fifo.Pop(0)
	if result == nil {
		t.Errorf("Pop(0) on empty FIFO returned nil, want empty slice")
	}
	if len(result) != 0 {
		t.Errorf("Pop(0) returned slice with len=%d, want 0", len(result))
	}

	// Test Pop(1) on empty buffer
	result = fifo.Pop(1)
	if result != nil {
		t.Errorf("Pop(1) on empty FIFO returned %v, want nil", result)
	}

	// Test Pop with large number on empty buffer
	result = fifo.Pop(1000)
	if result != nil {
		t.Errorf("Pop(1000) on empty FIFO returned %v, want nil", result)
	}

	t.Logf("EmptyBuffer test passed")
}

// TestAudioFIFO_BoundaryConditions tests edge cases with exact amounts.
func TestAudioFIFO_BoundaryConditions(t *testing.T) {
	fifo := NewAudioFIFO(1024)

	// Push exact amount
	samples := []float32{1.0, 2.0, 3.0, 4.0, 5.0}
	fifo.Push(samples)

	// Pop exact amount should succeed
	result := fifo.Pop(5)
	if result == nil {
		t.Fatalf("Pop(5) with exactly 5 samples returned nil")
	}
	if len(result) != 5 {
		t.Errorf("Pop(5) returned %d samples, want 5", len(result))
	}

	// Buffer should be empty
	if fifo.Available() != 0 {
		t.Errorf("After Pop(5), Available() = %d, want 0", fifo.Available())
	}

	// Pop on empty should return nil
	result = fifo.Pop(1)
	if result != nil {
		t.Errorf("Pop(1) on now-empty FIFO returned %v, want nil", result)
	}

	t.Logf("BoundaryConditions test passed")
}

// TestAudioFIFO_SequentialOperations tests multiple push/pop operations.
func TestAudioFIFO_SequentialOperations(t *testing.T) {
	fifo := NewAudioFIFO(1024)

	// Push and pop multiple times
	for round := 0; round < 5; round++ {
		// Push 10 samples
		samples := make([]float32, 10)
		for i := 0; i < 10; i++ {
			samples[i] = float32(round*10 + i)
		}
		fifo.Push(samples)

		if fifo.Available() != 10 {
			t.Errorf("Round %d: After Push(10), Available() = %d, want 10", round, fifo.Available())
		}

		// Pop 5 samples
		result := fifo.Pop(5)
		if result == nil {
			t.Fatalf("Round %d: Pop(5) returned nil", round)
		}
		if len(result) != 5 {
			t.Errorf("Round %d: Pop(5) returned %d samples, want 5", round, len(result))
		}

		// Should have 5 left
		if fifo.Available() != 5 {
			t.Errorf("Round %d: After Pop(5), Available() = %d, want 5", round, fifo.Available())
		}

		// Pop remaining 5
		result = fifo.Pop(5)
		if result == nil {
			t.Fatalf("Round %d: Pop(5) second time returned nil", round)
		}

		// Should be empty
		if fifo.Available() != 0 {
			t.Errorf("Round %d: After Pop(5) twice, Available() = %d, want 0", round, fifo.Available())
		}
	}

	t.Logf("SequentialOperations test passed (5 rounds)")
}

// TestAudioFIFO_LargeValues tests with large sample counts.
func TestAudioFIFO_LargeValues(t *testing.T) {
	fifo := NewAudioFIFO(1024)

	// Push a large number of samples
	largeCount := 100000
	samples := make([]float32, largeCount)
	for i := 0; i < largeCount; i++ {
		samples[i] = float32(i)
	}
	fifo.Push(samples)

	if fifo.Available() != largeCount {
		t.Errorf("After Push(%d), Available() = %d", largeCount, fifo.Available())
	}

	// Pop half
	result := fifo.Pop(largeCount / 2)
	if result == nil {
		t.Fatalf("Pop(%d) returned nil", largeCount/2)
	}
	if len(result) != largeCount/2 {
		t.Errorf("Pop(%d) returned %d samples", largeCount/2, len(result))
	}

	// Should have half left
	if fifo.Available() != largeCount/2 {
		t.Errorf("After Pop(%d), Available() = %d, want %d", largeCount/2, fifo.Available(), largeCount/2)
	}

	// Try to pop more than available
	result = fifo.Pop(largeCount)
	if result != nil {
		t.Errorf("Pop(%d) with only %d available returned non-nil", largeCount, largeCount/2)
	}

	t.Logf("LargeValues test passed: %d samples", largeCount)
}

// BenchmarkAudioFIFO_Pop measures allocation overhead for Pop() operations
// without returning slices to pool (simulates old behaviour).
func BenchmarkAudioFIFO_Pop(b *testing.B) {
	const samplesPerFrame = 1024 // AAC frame size

	// Pre-generate samples to avoid allocation during benchmark
	samples := make([]float32, samplesPerFrame)
	for i := range samples {
		samples[i] = float32(i) * 0.001
	}

	fifo := NewAudioFIFO(samplesPerFrame)

	// Sink slice to force escape analysis (prevents inlining optimisation)
	var results [][]float32

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		fifo.Push(samples)
		result := fifo.Pop(samplesPerFrame)
		if result == nil {
			b.Fatal("unexpected nil from Pop")
		}
		// Append to slice to prevent compiler from stack-allocating
		results = append(results, result)
	}

	// Prevent results from being optimised away
	if len(results) == 0 && b.N > 0 {
		b.Log("results used")
	}
}

// BenchmarkAudioFIFO_PopWithPool measures Pop() with proper slice pooling.
// This simulates real encoder behaviour where slices are returned to pool.
func BenchmarkAudioFIFO_PopWithPool(b *testing.B) {
	const samplesPerFrame = 1024 // AAC frame size

	// Pre-generate samples to avoid allocation during benchmark
	samples := make([]float32, samplesPerFrame)
	for i := range samples {
		samples[i] = float32(i) * 0.001
	}

	fifo := NewAudioFIFO(samplesPerFrame)

	// Sink to prevent compiler optimisation
	var sink float32

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		fifo.Push(samples)
		result := fifo.Pop(samplesPerFrame)
		if result == nil {
			b.Fatal("unexpected nil from Pop")
		}
		// Use the result (simulates writeMonoFloats/writeStereoFloats)
		sink += result[0]
		// Return to pool as encoder does
		fifo.ReturnSlice(result)
	}

	// Prevent sink from being optimised away
	if sink == 0 && b.N > 0 {
		b.Log("sink used")
	}
}
