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
	numSamples  int64
	numChannels int
	position    int64
}

// NewFLACDecoder creates a new FLAC decoder
func NewFLACDecoder(filename string) (*FLACDecoder, error) {
	// Use ffmpeg to get accurate metadata (sample rate, channels, duration)
	metadata, err := GetAudioMetadata(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get audio metadata: %w", err)
	}

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

	return &FLACDecoder{
		stream:      stream,
		file:        f,
		sampleRate:  metadata.SampleRate,
		numSamples:  metadata.NumSamples,
		numChannels: metadata.Channels,
		position:    0,
	}, nil
}

// ReadChunk reads the next chunk of samples
func (d *FLACDecoder) ReadChunk(numSamples int) ([]float64, error) {
	if d.position >= d.numSamples {
		return nil, io.EOF
	}

	// Adjust if requesting more samples than available
	if d.position+int64(numSamples) > d.numSamples {
		numSamples = int(d.numSamples - d.position)
	}

	samples := make([]float64, 0, numSamples)

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
				d.position += int64(len(samples))
				return samples, nil
			}
			return nil, fmt.Errorf("failed to parse FLAC frame: %w", err)
		}

		// FLAC frames contain one subframe per channel
		// We need to convert to mono by averaging channels or taking first channel
		frameSamples := len(frame.Subframes[0].Samples)
		
		for i := 0; i < frameSamples && len(samples) < numSamples; i++ {
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
			samples = append(samples, sample/maxVal)
		}
	}

	d.position += int64(len(samples))
	return samples, nil
}

// SampleRate returns the sample rate
func (d *FLACDecoder) SampleRate() int {
	return d.sampleRate
}

// NumSamples returns the total number of samples
func (d *FLACDecoder) NumSamples() int64 {
	return d.numSamples
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
