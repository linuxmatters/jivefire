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

// ReadNextFrame reads up to len(buf) samples from reader into the provided
// buffer. Returns the number of samples read. Returns (0, io.EOF) when no
// samples are available. Returns (n, nil) for partial frames at end of file.
func ReadNextFrame(reader *StreamingReader, buf []float64) (int, error) {
	var total int
	for total < len(buf) {
		chunk, err := reader.ReadChunk(len(buf) - total)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if total == 0 {
					return 0, io.EOF
				}
				break
			}
			return 0, fmt.Errorf("reading audio frame: %w", err)
		}
		copy(buf[total:], chunk)
		total += len(chunk)
	}
	return total, nil
}
