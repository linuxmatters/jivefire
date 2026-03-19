package audio

import (
	"errors"
	"io"
	"testing"
)

func TestFillFFTBufferNormal(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	buf := make([]float64, 2048)
	n, err := FillFFTBuffer(reader, buf)
	if err != nil {
		t.Fatalf("FillFFTBuffer returned error: %v", err)
	}
	if n != 2048 {
		t.Errorf("Expected 2048 samples, got %d", n)
	}
}

func TestFillFFTBufferPartial(t *testing.T) {
	// Read most of the file first, then try to fill a large buffer
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain most of the file
	drain := make([]float64, 4096)
	for {
		n, err := FillFFTBuffer(reader, drain)
		if err != nil {
			t.Fatalf("Unexpected error draining: %v", err)
		}
		if n < len(drain) {
			// Partial read means we hit EOF partway through
			break
		}
	}

	// Next fill should return 0 (EOF already reached)
	buf := make([]float64, 2048)
	n, err := FillFFTBuffer(reader, buf)
	if err != nil {
		t.Fatalf("FillFFTBuffer returned error: %v", err)
	}
	if n != 0 {
		t.Errorf("Expected 0 samples after EOF, got %d", n)
	}
}

func TestFillFFTBufferEmpty(t *testing.T) {
	// Reading after full drain should return (0, nil)
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain completely
	drain := make([]float64, 8192)
	for {
		n, err := FillFFTBuffer(reader, drain)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if n == 0 {
			break
		}
	}

	buf := make([]float64, 2048)
	n, err := FillFFTBuffer(reader, buf)
	if err != nil {
		t.Fatalf("FillFFTBuffer returned error: %v", err)
	}
	if n != 0 {
		t.Errorf("Expected (0, nil) on empty, got (%d, nil)", n)
	}
}

func TestReadNextFrameNormal(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	samples, err := ReadNextFrame(reader, 1024)
	if err != nil {
		t.Fatalf("ReadNextFrame returned error: %v", err)
	}
	if len(samples) != 1024 {
		t.Errorf("Expected 1024 samples, got %d", len(samples))
	}
}

func TestReadNextFramePartialAtEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain most of the file
	for {
		samples, err := ReadNextFrame(reader, 4096)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if len(samples) < 4096 {
			// Got a partial frame at EOF - this is the expected partial case
			if len(samples) == 0 {
				t.Error("Expected partial samples, got 0")
			}
			break
		}
	}
}

func TestReadNextFrameImmediateEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain completely
	for {
		_, err := ReadNextFrame(reader, 8192)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Now should get immediate EOF
	samples, err := ReadNextFrame(reader, 1024)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Expected io.EOF, got err=%v samples=%d", err, len(samples))
	}
	if samples != nil {
		t.Errorf("Expected nil samples on EOF, got %d", len(samples))
	}
}
