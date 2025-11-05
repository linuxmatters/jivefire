package audio

import (
	"fmt"

	ffmpeg "github.com/csnewman/ffmpeg-go"
)

// AudioMetadata holds information about an audio file
type AudioMetadata struct {
	SampleRate int
	Channels   int
	NumSamples int64
	Duration   float64 // in seconds
}

// GetAudioMetadata uses ffmpeg to extract accurate audio file metadata
func GetAudioMetadata(filename string) (*AudioMetadata, error) {
	// Open audio file
	var inputCtx *ffmpeg.AVFormatContext
	audioPath := ffmpeg.ToCStr(filename)
	defer audioPath.Free()

	ret, err := ffmpeg.AVFormatOpenInput(&inputCtx, audioPath, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	if ret < 0 {
		return nil, fmt.Errorf("failed to open audio file: %d", ret)
	}
	defer ffmpeg.AVFormatCloseInput(&inputCtx)

	ret, err = ffmpeg.AVFormatFindStreamInfo(inputCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to find stream info: %w", err)
	}
	if ret < 0 {
		return nil, fmt.Errorf("failed to find stream info: %d", ret)
	}

	// Find audio stream
	audioStreamIdx := -1
	streams := inputCtx.Streams()
	for i := uintptr(0); i < uintptr(inputCtx.NbStreams()); i++ {
		stream := streams.Get(i)
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			audioStreamIdx = int(i)
			break
		}
	}
	if audioStreamIdx == -1 {
		return nil, fmt.Errorf("no audio stream found in file")
	}

	audioStream := streams.Get(uintptr(audioStreamIdx))
	codecpar := audioStream.Codecpar()

	// Extract metadata
	sampleRate := int(codecpar.SampleRate())
	channels := int(codecpar.Channels())

	// Calculate duration and total samples
	// Duration is in stream time_base units
	duration := float64(audioStream.Duration()) * float64(audioStream.TimeBase().Num()) / float64(audioStream.TimeBase().Den())
	numSamples := int64(duration * float64(sampleRate))

	return &AudioMetadata{
		SampleRate: sampleRate,
		Channels:   channels,
		NumSamples: numSamples,
		Duration:   duration,
	}, nil
}
