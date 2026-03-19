package audio

import (
	"fmt"
)

// StreamingReader provides chunk-based audio reading for multiple formats
type StreamingReader struct {
	decoder Decoder
}

// NewStreamingReader creates a streaming audio reader for the given file.
// Uses FFmpeg decoder for broad format support (MP3, FLAC, WAV, OGG, AAC, etc.)
func NewStreamingReader(filename string) (*StreamingReader, error) {
	decoder, err := NewFFmpegDecoder(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	return &StreamingReader{decoder: decoder}, nil
}

// ReadChunk reads next chunk of samples, returns nil when EOF
func (r *StreamingReader) ReadChunk(numSamples int) ([]float64, error) {
	return r.decoder.ReadChunk(numSamples)
}

// Close closes the underlying file
func (r *StreamingReader) Close() error {
	return r.decoder.Close()
}

// SampleRate returns the sample rate
func (r *StreamingReader) SampleRate() int {
	return r.decoder.SampleRate()
}
