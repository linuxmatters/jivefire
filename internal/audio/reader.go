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

// NewStreamingReader creates a streaming audio reader for the given file.
// Uses FFmpeg decoder for broad format support (MP3, FLAC, WAV, OGG, AAC, etc.)
// Falls back to pure Go decoders if FFmpeg fails (shouldn't happen normally).
func NewStreamingReader(filename string) (*StreamingReader, error) {
	// Try FFmpeg decoder first - supports all common audio formats
	decoder, err := NewFFmpegDecoder(filename)
	if err == nil {
		return &StreamingReader{decoder: decoder}, nil
	}

	// Fallback to pure Go decoders for specific formats
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".wav":
		decoder, err := NewWAVDecoder(filename)
		if err != nil {
			return nil, err
		}
		return &StreamingReader{decoder: decoder}, nil
	case ".mp3":
		decoder, err := NewMP3Decoder(filename)
		if err != nil {
			return nil, err
		}
		return &StreamingReader{decoder: decoder}, nil
	case ".flac":
		decoder, err := NewFLACDecoder(filename)
		if err != nil {
			return nil, err
		}
		return &StreamingReader{decoder: decoder}, nil
	default:
		return nil, fmt.Errorf("unsupported audio format: %s (FFmpeg error: %v)", ext, err)
	}
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
