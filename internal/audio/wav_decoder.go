package audio

import (
	"fmt"
	"io"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// WAVDecoder implements AudioDecoder for WAV files
type WAVDecoder struct {
	decoder    *wav.Decoder
	file       *os.File
	sampleRate int
	bitDepth   int
	numChans   int
}

// NewWAVDecoder creates a new WAV decoder
func NewWAVDecoder(filename string) (*WAVDecoder, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	decoder := wav.NewDecoder(f)
	if !decoder.IsValidFile() {
		f.Close()
		return nil, fmt.Errorf("invalid WAV file")
	}

	// Get format info without reading all samples
	if err := decoder.FwdToPCM(); err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to seek to PCM data: %w", err)
	}

	return &WAVDecoder{
		decoder:    decoder,
		file:       f,
		sampleRate: int(decoder.SampleRate),
		bitDepth:   int(decoder.BitDepth),
		numChans:   int(decoder.NumChans),
	}, nil
}

// ReadChunk reads the next chunk of samples
func (d *WAVDecoder) ReadChunk(numSamples int) ([]float64, error) {
	// Create buffer for reading - need numSamples × numChannels for interleaved data
	bufSize := numSamples * d.numChans
	intBuf := &audio.IntBuffer{
		Data: make([]int, bufSize),
		Format: &audio.Format{
			NumChannels: d.numChans,
			SampleRate:  d.sampleRate,
		},
	}

	// Read PCM data
	n, err := d.decoder.PCMBuffer(intBuf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read PCM buffer: %w", err)
	}

	if n == 0 {
		return nil, io.EOF
	}

	// Convert to float64 and handle stereo/mono
	maxVal := float64(audio.IntMaxSignedValue(d.bitDepth))

	if d.numChans == 1 {
		// Mono - direct conversion
		samples := make([]float64, n)
		for i := 0; i < n; i++ {
			samples[i] = float64(intBuf.Data[i]) / maxVal
		}
		return samples, nil
	}

	// Multi-channel - downmix to mono by averaging channels
	// n samples with d.numChans channels = n/d.numChans time samples
	numTimeSamples := n / d.numChans
	samples := make([]float64, numTimeSamples)

	for i := 0; i < numTimeSamples; i++ {
		var sum float64
		for ch := 0; ch < d.numChans; ch++ {
			sum += float64(intBuf.Data[i*d.numChans+ch]) / maxVal
		}
		samples[i] = sum / float64(d.numChans)
	}

	return samples, nil
}

// SampleRate returns the sample rate
func (d *WAVDecoder) SampleRate() int {
	return d.sampleRate
}

// NumChannels returns the number of audio channels
func (d *WAVDecoder) NumChannels() int {
	return d.numChans
}

// Close closes the decoder and releases resources
func (d *WAVDecoder) Close() error {
	if d.file != nil {
		return d.file.Close()
	}
	return nil
}
