package audio

import (
	"errors"
	"fmt"
	"io"
)

// FillFFTBuffer reads up to len(buf) samples from reader via repeated ReadChunk
// calls. Returns the number of samples read. Returns (0, nil) on immediate EOF,
// allowing callers to decide whether that is an error.
func FillFFTBuffer(reader *StreamingReader, buf []float64) (int, error) {
	var total int
	for total < len(buf) {
		chunk, err := reader.ReadChunk(len(buf) - total)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return total, fmt.Errorf("reading audio chunk: %w", err)
		}
		copy(buf[total:], chunk)
		total += len(chunk)
	}
	return total, nil
}

// ReadNextFrame reads up to samplesPerFrame samples from reader. Returns
// (nil, io.EOF) when no samples are available. Returns (partial, nil) for
// partial frames at end of file.
func ReadNextFrame(reader *StreamingReader, samplesPerFrame int) ([]float64, error) {
	var samples []float64
	for len(samples) < samplesPerFrame {
		chunk, err := reader.ReadChunk(samplesPerFrame - len(samples))
		if err != nil {
			if errors.Is(err, io.EOF) {
				if len(samples) == 0 {
					return nil, io.EOF
				}
				break
			}
			return nil, fmt.Errorf("reading audio frame: %w", err)
		}
		samples = append(samples, chunk...)
	}
	return samples, nil
}
