package audio

import "io"

// Decoder defines the interface for all audio format decoders
type Decoder interface {
	// ReadChunk reads the next chunk of samples as float64
	// Returns nil when EOF is reached
	ReadChunk(numSamples int) ([]float64, error)

	// SampleRate returns the audio sample rate in Hz
	SampleRate() int

	// NumChannels returns the number of audio channels (1=mono, 2=stereo)
	NumChannels() int

	// Close closes the decoder and releases resources
	Close() error
}

// Ensure io.EOF is available for decoder implementations
var EOF = io.EOF
