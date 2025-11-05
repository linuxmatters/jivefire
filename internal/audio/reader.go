package audio

import (
	"fmt"
	"path/filepath"
	"strings"
)

// StreamingReader provides chunk-based audio reading for multiple formats
type StreamingReader struct {
	decoder AudioDecoder
}

// NewStreamingReader creates a streaming audio reader for the given file
// Automatically detects format based on file extension (.wav, .mp3, .flac)
func NewStreamingReader(filename string) (*StreamingReader, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	var decoder AudioDecoder
	var err error

	switch ext {
	case ".wav":
		decoder, err = NewWAVDecoder(filename)
	case ".mp3":
		decoder, err = NewMP3Decoder(filename)
	case ".flac":
		decoder, err = NewFLACDecoder(filename)
	default:
		return nil, fmt.Errorf("unsupported audio format: %s (supported: .wav, .mp3, .flac)", ext)
	}

	if err != nil {
		return nil, err
	}

	return &StreamingReader{
		decoder: decoder,
	}, nil
}

// ReadChunk reads next chunk of samples, returns nil when EOF
func (r *StreamingReader) ReadChunk(numSamples int) ([]float64, error) {
	return r.decoder.ReadChunk(numSamples)
}

// SeekToSample repositions reader to sample position
// Note: Not fully implemented - requires complex file format calculations
// For 2-pass processing, we simply close and reopen which is simpler
func (r *StreamingReader) SeekToSample(samplePos int64) error {
	return fmt.Errorf("seek not implemented - use Close and create new reader")
}

// Close closes the underlying file
func (r *StreamingReader) Close() error {
	return r.decoder.Close()
}

// NumSamples returns total sample count
func (r *StreamingReader) NumSamples() int64 {
	return r.decoder.NumSamples()
}

// SampleRate returns the sample rate
func (r *StreamingReader) SampleRate() int {
	return r.decoder.SampleRate()
}

// NumChannels returns the number of audio channels
func (r *StreamingReader) NumChannels() int {
	return r.decoder.NumChannels()
}
