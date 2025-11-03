package audio

import (
	"fmt"
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
