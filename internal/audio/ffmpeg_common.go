package audio

import (
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// openAudioFormatCtx opens an audio file, finds stream info, and locates the
// first audio stream. The caller is responsible for closing the returned
// format context via AVFormatCloseInput.
func openAudioFormatCtx(filename string) (*ffmpeg.AVFormatContext, int, error) {
	var formatCtx *ffmpeg.AVFormatContext

	path := ffmpeg.ToCStr(filename)
	defer path.Free()

	ret, err := ffmpeg.AVFormatOpenInput(&formatCtx, path, nil, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open audio file: %w", err)
	}
	if ret < 0 {
		return nil, 0, fmt.Errorf("failed to open audio file: error code %d", ret)
	}

	ret, err = ffmpeg.AVFormatFindStreamInfo(formatCtx, nil)
	if err != nil {
		ffmpeg.AVFormatCloseInput(&formatCtx)
		return nil, 0, fmt.Errorf("failed to find stream info: %w", err)
	}
	if ret < 0 {
		ffmpeg.AVFormatCloseInput(&formatCtx)
		return nil, 0, fmt.Errorf("failed to find stream info: error code %d", ret)
	}

	audioStreamIdx := -1
	streams := formatCtx.Streams()
	for i := uintptr(0); i < uintptr(formatCtx.NbStreams()); i++ {
		stream := streams.Get(i)
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			audioStreamIdx = int(i)
			break
		}
	}
	if audioStreamIdx == -1 {
		ffmpeg.AVFormatCloseInput(&formatCtx)
		return nil, 0, fmt.Errorf("no audio stream found in file")
	}

	return formatCtx, audioStreamIdx, nil
}
