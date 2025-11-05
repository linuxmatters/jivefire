package audio

import (
	"fmt"
	"io"
	"os"

	"github.com/hajimehoshi/go-mp3"
)

// MP3Decoder implements AudioDecoder for MP3 files
type MP3Decoder struct {
	decoder     *mp3.Decoder
	file        *os.File
	sampleRate  int
	numSamples  int64
	numChannels int
	position    int64
}

// NewMP3Decoder creates a new MP3 decoder
func NewMP3Decoder(filename string) (*MP3Decoder, error) {
	// Use ffmpeg to get accurate metadata (sample rate, channels, duration)
	metadata, err := GetAudioMetadata(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get audio metadata: %w", err)
	}

	// Open file for MP3 decoding
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}

	decoder, err := mp3.NewDecoder(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to create MP3 decoder: %w", err)
	}

	return &MP3Decoder{
		decoder:     decoder,
		file:        f,
		sampleRate:  metadata.SampleRate,
		numSamples:  metadata.NumSamples,
		numChannels: metadata.Channels,
		position:    0,
	}, nil
}

// ReadChunk reads the next chunk of samples
func (d *MP3Decoder) ReadChunk(numSamples int) ([]float64, error) {
	if d.position >= d.numSamples {
		return nil, io.EOF
	}

	// Adjust if requesting more samples than available
	if d.position+int64(numSamples) > d.numSamples {
		numSamples = int(d.numSamples - d.position)
	}

	// MP3 decoder outputs 16-bit signed LE samples
	// We need to read bytes and convert to float64
	buf := make([]byte, numSamples*2) // 2 bytes per sample

	n, err := d.decoder.Read(buf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read MP3 data: %w", err)
	}

	if n == 0 {
		return nil, io.EOF
	}

	// Convert bytes to float64 samples
	// MP3 outputs 16-bit signed little-endian
	samplesRead := n / 2
	samples := make([]float64, samplesRead)

	for i := 0; i < samplesRead; i++ {
		// Read 16-bit signed little-endian
		int16val := int16(buf[i*2]) | (int16(buf[i*2+1]) << 8)
		// Convert to float64 in range [-1.0, 1.0]
		samples[i] = float64(int16val) / 32768.0
	}

	d.position += int64(samplesRead)
	return samples, nil
}

// SampleRate returns the sample rate
func (d *MP3Decoder) SampleRate() int {
	return d.sampleRate
}

// NumSamples returns the total number of samples
func (d *MP3Decoder) NumSamples() int64 {
	return d.numSamples
}

// NumChannels returns the number of audio channels
func (d *MP3Decoder) NumChannels() int {
	return d.numChannels
}

// Close closes the decoder and releases resources
func (d *MP3Decoder) Close() error {
	if d.file != nil {
		return d.file.Close()
	}
	return nil
}
