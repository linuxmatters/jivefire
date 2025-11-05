package audio

import (
	"fmt"
	"io"
	"os"

	"github.com/mewkiz/flac"
)

// FLACDecoder implements AudioDecoder for FLAC files
type FLACDecoder struct {
	stream      *flac.Stream
	file        *os.File
	sampleRate  int
	numChannels int
	buffer      []float64 // Buffered samples from previous FLAC frame
}

// NewFLACDecoder creates a new FLAC decoder
func NewFLACDecoder(filename string) (*FLACDecoder, error) {
	// Open file for FLAC decoding
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	// Parse FLAC stream - reads signature and StreamInfo block
	stream, err := flac.New(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to create FLAC decoder: %w", err)
	}

	// Get format info from FLAC StreamInfo block
	info := stream.Info

	return &FLACDecoder{
		stream:      stream,
		file:        f,
		sampleRate:  int(info.SampleRate),
		numChannels: int(info.NChannels),
	}, nil
}

// ReadChunk reads the next chunk of samples
func (d *FLACDecoder) ReadChunk(numSamples int) ([]float64, error) {
	samples := make([]float64, 0, numSamples)

	// First, use any buffered samples from previous frame
	if len(d.buffer) > 0 {
		takeFromBuffer := numSamples
		if takeFromBuffer > len(d.buffer) {
			takeFromBuffer = len(d.buffer)
		}
		samples = append(samples, d.buffer[:takeFromBuffer]...)
		d.buffer = d.buffer[takeFromBuffer:]
	}

	// Read FLAC frames until we have enough samples
	for len(samples) < numSamples {
		// Parse next frame including audio samples
		frame, err := d.stream.ParseNext()
		if err != nil {
			if err == io.EOF {
				// End of stream - return what we have
				if len(samples) == 0 {
					return nil, io.EOF
				}
				return samples, nil
			}
			return nil, fmt.Errorf("failed to parse FLAC frame: %w", err)
		}

		// FLAC frames contain one subframe per channel
		// Convert all frame samples to float64 and normalize
		frameSamples := len(frame.Subframes[0].Samples)

		for i := 0; i < frameSamples; i++ {
			var sample float64

			if len(frame.Subframes) == 1 {
				// Mono - use directly
				sample = float64(frame.Subframes[0].Samples[i])
			} else {
				// Multi-channel - average all channels for downmix
				var sum int64
				for _, subframe := range frame.Subframes {
					sum += int64(subframe.Samples[i])
				}
				sample = float64(sum) / float64(len(frame.Subframes))
			}

			// Normalize to [-1.0, 1.0] based on bits per sample
			// FLAC supports 4-32 bits per sample
			bitsPerSample := frame.BitsPerSample
			maxVal := float64(int64(1) << (bitsPerSample - 1))
			normalizedSample := sample / maxVal

			// Add to output if we need more samples
			if len(samples) < numSamples {
				samples = append(samples, normalizedSample)
			} else {
				// Buffer the rest for next call
				d.buffer = append(d.buffer, normalizedSample)
			}
		}
	}

	return samples, nil
}

// SampleRate returns the sample rate
func (d *FLACDecoder) SampleRate() int {
	return d.sampleRate
}

// NumChannels returns the number of audio channels
func (d *FLACDecoder) NumChannels() int {
	return d.numChannels
}

// Close closes the decoder and releases resources
func (d *FLACDecoder) Close() error {
	if d.stream != nil {
		d.stream.Close()
	}
	if d.file != nil {
		return d.file.Close()
	}
	return nil
}
