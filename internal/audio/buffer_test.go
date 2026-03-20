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

	buf := make([]float64, 1024)
	n, err := ReadNextFrame(reader, buf)
	if err != nil {
		t.Fatalf("ReadNextFrame returned error: %v", err)
	}
	if n != 1024 {
		t.Errorf("Expected 1024 samples, got %d", n)
	}
}

func TestReadNextFramePartialAtEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain most of the file, tracking whether we saw a partial frame
	buf := make([]float64, 4096)
	sawPartial := false
	for {
		n, err := ReadNextFrame(reader, buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if n < len(buf) {
			// Got a partial frame at EOF
			if n == 0 {
				t.Error("Expected partial samples, got 0")
			}
			sawPartial = true
			break
		}
	}

	if !sawPartial {
		t.Error("Expected to observe a partial frame before EOF, but none was returned")
	}
}

func TestReadNextFrameImmediateEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create reader: %v", err)
	}
	defer reader.Close()

	// Drain completely
	buf := make([]float64, 8192)
	for {
		_, err := ReadNextFrame(reader, buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Now should get immediate EOF
	smallBuf := make([]float64, 1024)
	n, err := ReadNextFrame(reader, smallBuf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Expected io.EOF, got err=%v n=%d", err, n)
	}
	if n != 0 {
		t.Errorf("Expected 0 samples on EOF, got %d", n)
	}
}
