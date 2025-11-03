package audio

import (
	"fmt"
	"io"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// ReadWAV reads a WAV file and returns samples as float64 slice
func ReadWAV(filename string) ([]float64, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	decoder := wav.NewDecoder(f)
	if !decoder.IsValidFile() {
		return nil, fmt.Errorf("invalid WAV file")
	}

	buf, err := decoder.FullPCMBuffer()
	if err != nil {
		return nil, err
	}

	// Convert to float64
	samples := make([]float64, len(buf.Data))
	for i, s := range buf.Data {
		samples[i] = float64(s) / float64(audio.IntMaxSignedValue(int(decoder.BitDepth)))
	}

	return samples, nil
}

// StreamingReader provides chunk-based WAV reading
type StreamingReader struct {
	decoder    *wav.Decoder
	file       *os.File
	sampleRate int
	bitDepth   int
	numSamples int64
	position   int64
}

// NewStreamingReader creates a streaming WAV reader
func NewStreamingReader(filename string) (*StreamingReader, error) {
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

	// Calculate total samples from file size and format
	// PCMLen gives us the length of PCM data in bytes
	bytesPerSample := int64(decoder.BitDepth / 8)
	numChannels := int64(decoder.NumChans)
	totalSamples := int64(decoder.PCMLen()) / (bytesPerSample * numChannels)

	return &StreamingReader{
		decoder:    decoder,
		file:       f,
		sampleRate: int(decoder.SampleRate),
		bitDepth:   int(decoder.BitDepth),
		numSamples: totalSamples,
		position:   0,
	}, nil
}

// ReadChunk reads next chunk of samples, returns nil when EOF
func (r *StreamingReader) ReadChunk(numSamples int) ([]float64, error) {
	if r.position >= r.numSamples {
		return nil, io.EOF
	}

	// Adjust if requesting more samples than available
	if r.position+int64(numSamples) > r.numSamples {
		numSamples = int(r.numSamples - r.position)
	}

	// Create buffer for reading
	intBuf := &audio.IntBuffer{
		Data: make([]int, numSamples),
		Format: &audio.Format{
			NumChannels: int(r.decoder.NumChans),
			SampleRate:  int(r.decoder.SampleRate),
		},
	}

	// Read PCM data
	n, err := r.decoder.PCMBuffer(intBuf)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("failed to read PCM buffer: %w", err)
	}

	if n == 0 {
		return nil, io.EOF
	}

	// Convert to float64
	samples := make([]float64, n)
	maxVal := float64(audio.IntMaxSignedValue(r.bitDepth))
	for i := 0; i < n; i++ {
		samples[i] = float64(intBuf.Data[i]) / maxVal
	}

	r.position += int64(n)
	return samples, nil
}

// SeekToSample repositions reader to sample position
// Note: Not fully implemented - requires complex file format calculations
// For 2-pass processing, we simply close and reopen which is simpler
func (r *StreamingReader) SeekToSample(samplePos int64) error {
	return fmt.Errorf("seek not implemented - use Close and create new reader")
}

// Close closes the underlying file
func (r *StreamingReader) Close() error {
	if r.file != nil {
		return r.file.Close()
	}
	return nil
}

// NumSamples returns total sample count
func (r *StreamingReader) NumSamples() int64 {
	return r.numSamples
}

// SampleRate returns the sample rate
func (r *StreamingReader) SampleRate() int {
	return r.sampleRate
}
