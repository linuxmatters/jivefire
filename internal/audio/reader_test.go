package audio

import (
	"io"
	"testing"
)

func TestNewStreamingReader(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	if reader.NumSamples() <= 0 {
		t.Errorf("Expected positive number of samples, got %d", reader.NumSamples())
	}

	if reader.SampleRate() != 44100 {
		t.Errorf("Expected sample rate 44100, got %d", reader.SampleRate())
	}

	t.Logf("Successfully opened WAV file: %d samples at %d Hz", reader.NumSamples(), reader.SampleRate())
}

func TestNewStreamingReaderInvalidFile(t *testing.T) {
	_, err := NewStreamingReader("nonexistent.wav")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestStreamingReaderReadChunk(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Read a chunk of 2048 samples
	chunk, err := reader.ReadChunk(2048)
	if err != nil {
		t.Fatalf("Failed to read chunk: %v", err)
	}

	if len(chunk) != 2048 {
		t.Errorf("Expected chunk size 2048, got %d", len(chunk))
	}

	// Check that values are normalized float64 (between -1.0 and 1.0)
	for i, sample := range chunk {
		if sample < -1.0 || sample > 1.0 {
			t.Errorf("Sample %d out of range: %f (should be between -1.0 and 1.0)", i, sample)
		}
	}

	t.Logf("Successfully read chunk of %d samples", len(chunk))
}

func TestStreamingReaderMultipleChunks(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	chunkSize := 2048
	totalRead := int64(0)
	chunkCount := 0

	for {
		chunk, err := reader.ReadChunk(chunkSize)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Error reading chunk %d: %v", chunkCount, err)
		}

		if len(chunk) > chunkSize {
			t.Errorf("Chunk %d is larger than requested: %d > %d", chunkCount, len(chunk), chunkSize)
		}

		totalRead += int64(len(chunk))
		chunkCount++

		// Limit test to avoid reading entire file
		if chunkCount >= 10 {
			break
		}
	}

	if totalRead == 0 {
		t.Error("No samples were read")
	}

	t.Logf("Successfully read %d chunks (%d total samples)", chunkCount, totalRead)
}

func TestStreamingReaderEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Read all samples
	totalSamples := reader.NumSamples()
	chunkSize := 4096

	for {
		_, err := reader.ReadChunk(chunkSize)
		if err == io.EOF {
			// Expected EOF
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Try reading again - should get EOF immediately
	_, err = reader.ReadChunk(chunkSize)
	if err != io.EOF {
		t.Errorf("Expected EOF on second read past end, got: %v", err)
	}

	t.Logf("EOF handling works correctly after reading %d samples", totalSamples)
}

func TestStreamingReaderClose(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}

	err = reader.Close()
	if err != nil {
		t.Errorf("Failed to close reader: %v", err)
	}

	// Second close should not panic
	err = reader.Close()
	if err != nil {
		t.Logf("Second close returned error (acceptable): %v", err)
	}
}

func TestStreamingReaderVsFullRead(t *testing.T) {
	// Compare streaming reader with full buffer read
	fullSamples, err := ReadWAV("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to read full WAV: %v", err)
	}

	reader, err := NewStreamingReader("../../testdata/dream.wav")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Read all samples via streaming
	var streamedSamples []float64
	chunkSize := 2048

	for {
		chunk, err := reader.ReadChunk(chunkSize)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Error reading chunk: %v", err)
		}
		streamedSamples = append(streamedSamples, chunk...)
	}

	// Compare counts
	if len(fullSamples) != len(streamedSamples) {
		t.Errorf("Sample count mismatch: full=%d, streamed=%d", len(fullSamples), len(streamedSamples))
	}

	// Compare first 100 samples (should be identical)
	compareCount := 100
	if len(fullSamples) < compareCount {
		compareCount = len(fullSamples)
	}

	for i := 0; i < compareCount; i++ {
		if fullSamples[i] != streamedSamples[i] {
			t.Errorf("Sample %d mismatch: full=%f, streamed=%f", i, fullSamples[i], streamedSamples[i])
		}
	}

	t.Logf("Streaming reader matches full read: %d samples verified", compareCount)
}
