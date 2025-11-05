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
	numChannels int
}

// NewMP3Decoder creates a new MP3 decoder
func NewMP3Decoder(filename string) (*MP3Decoder, error) {
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
		sampleRate:  decoder.SampleRate(),
		numChannels: 2, // go-mp3 always outputs stereo
	}, nil
}

// ReadChunk reads the next chunk of samples
func (d *MP3Decoder) ReadChunk(numSamples int) ([]float64, error) {
	// go-mp3 always outputs interleaved stereo: L0 R0 L1 R1 L2 R2 ...
	// Each channel sample is 16-bit (2 bytes), so 4 bytes per time sample
	// We need numSamples mono samples, which means reading numSamples stereo frames
	buf := make([]byte, numSamples*4) // 4 bytes per stereo sample (2 bytes × 2 channels)

	n, err := d.decoder.Read(buf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read MP3 data: %w", err)
	}

	if n == 0 {
		return nil, io.EOF
	}

	// Convert bytes to float64 samples, converting stereo to mono by averaging
	// n bytes = n/4 stereo samples = n/4 mono samples after averaging
	stereoSamplesRead := n / 4
	samples := make([]float64, stereoSamplesRead)

	for i := 0; i < stereoSamplesRead; i++ {
		// Read left channel (16-bit signed little-endian)
		leftInt16 := int16(buf[i*4]) | (int16(buf[i*4+1]) << 8)
		left := float64(leftInt16) / 32768.0

		// Read right channel (16-bit signed little-endian)
		rightInt16 := int16(buf[i*4+2]) | (int16(buf[i*4+3]) << 8)
		right := float64(rightInt16) / 32768.0

		// Average to mono
		samples[i] = (left + right) / 2.0
	}

	return samples, nil
}

// SampleRate returns the sample rate
func (d *MP3Decoder) SampleRate() int {
	return d.sampleRate
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
