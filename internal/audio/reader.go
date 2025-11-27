package audio

import (
	"fmt"
)

// StreamingReader provides chunk-based audio reading for multiple formats
type StreamingReader struct {
	decoder AudioDecoder
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

// SeekToSample repositions reader to sample position.
// Works with FFmpegDecoder; pure Go decoders do not support seeking.
func (r *StreamingReader) SeekToSample(samplePos int64) error {
	// Check if decoder supports seeking (FFmpegDecoder does)
	if seeker, ok := r.decoder.(*FFmpegDecoder); ok {
		return seeker.SeekToSample(samplePos)
	}
	return fmt.Errorf("seek not supported by this decoder - use Close and create new reader")
}

// Close closes the underlying file
func (r *StreamingReader) Close() error {
	return r.decoder.Close()
}

// SampleRate returns the sample rate
func (r *StreamingReader) SampleRate() int {
	return r.decoder.SampleRate()
}

// NumChannels returns the number of audio channels
func (r *StreamingReader) NumChannels() int {
	return r.decoder.NumChannels()
}
