package audio

import (
	"errors"
	"fmt"
	"io"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// FFmpegDecoder implements AudioDecoder using FFmpeg's libavformat/libavcodec.
// This provides support for any audio format FFmpeg can decode.
type FFmpegDecoder struct {
	formatCtx   *ffmpeg.AVFormatContext
	codecCtx    *ffmpeg.AVCodecContext
	streamIndex int
	packet      *ffmpeg.AVPacket
	frame       *ffmpeg.AVFrame
	sampleRate  int
	channels    int

	// Buffer for leftover samples from previous decode
	sampleBuffer []float64
}

// NewFFmpegDecoder creates a new FFmpeg-based audio decoder.
// Supports any audio format FFmpeg can decode (MP3, FLAC, WAV, OGG, AAC, etc.)
func NewFFmpegDecoder(filename string) (*FFmpegDecoder, error) {
	d := &FFmpegDecoder{
		sampleBuffer: make([]float64, 0, 8192),
	}

	// Open input file
	path := ffmpeg.ToCStr(filename)
	defer path.Free()

	ret, err := ffmpeg.AVFormatOpenInput(&d.formatCtx, path, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	if ret < 0 {
		return nil, fmt.Errorf("failed to open audio file: error code %d", ret)
	}

	// Find stream info
	ret, err = ffmpeg.AVFormatFindStreamInfo(d.formatCtx, nil)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to find stream info: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to find stream info: error code %d", ret)
	}

	// Find audio stream
	d.streamIndex = -1
	streams := d.formatCtx.Streams()
	for i := uintptr(0); i < uintptr(d.formatCtx.NbStreams()); i++ {
		stream := streams.Get(i)
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			d.streamIndex = int(i)
			break
		}
	}
	if d.streamIndex == -1 {
		d.Close()
		return nil, fmt.Errorf("no audio stream found in file")
	}

	audioStream := streams.Get(uintptr(d.streamIndex))

	// Find decoder
	decoder := ffmpeg.AVCodecFindDecoder(audioStream.Codecpar().CodecId())
	if decoder == nil {
		d.Close()
		return nil, fmt.Errorf("audio decoder not found for codec ID %d", audioStream.Codecpar().CodecId())
	}

	// Allocate codec context
	d.codecCtx = ffmpeg.AVCodecAllocContext3(decoder)
	if d.codecCtx == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	// Copy codec parameters
	ret, err = ffmpeg.AVCodecParametersToContext(d.codecCtx, audioStream.Codecpar())
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: error code %d", ret)
	}

	// Open codec
	ret, err = ffmpeg.AVCodecOpen2(d.codecCtx, decoder, nil)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: error code %d", ret)
	}

	// Store audio properties
	d.sampleRate = d.codecCtx.SampleRate()
	d.channels = d.codecCtx.ChLayout().NbChannels()

	// Validate format support
	sampleFmt := int32(d.codecCtx.SampleFmt())
	supportedFormats := map[int32]bool{
		1: true, // AVSampleFmtS16 - 16-bit signed interleaved
		2: true, // AVSampleFmtS32 - 32-bit signed interleaved
		3: true, // AVSampleFmtFlt - 32-bit float interleaved
		6: true, // AVSampleFmtS16P - 16-bit signed planar
		7: true, // AVSampleFmtS32P - 32-bit signed planar
		8: true, // AVSampleFmtFltp - 32-bit float planar
	}
	if !supportedFormats[sampleFmt] {
		d.Close()
		return nil, fmt.Errorf("unsupported sample format: %d", sampleFmt)
	}

	// Allocate packet and frame
	d.packet = ffmpeg.AVPacketAlloc()
	if d.packet == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate packet")
	}

	d.frame = ffmpeg.AVFrameAlloc()
	if d.frame == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate frame")
	}

	return d, nil
}

// ReadChunk reads the next chunk of samples as float64.
// Stereo input is automatically downmixed to mono.
// Returns io.EOF when no more samples are available.
func (d *FFmpegDecoder) ReadChunk(numSamples int) ([]float64, error) {
	// First, try to satisfy request from buffer
	if len(d.sampleBuffer) >= numSamples {
		result := make([]float64, numSamples)
		copy(result, d.sampleBuffer[:numSamples])
		d.sampleBuffer = d.sampleBuffer[numSamples:]
		return result, nil
	}

	// Need to decode more samples
	for len(d.sampleBuffer) < numSamples {
		// Read packet
		ret, err := ffmpeg.AVReadFrame(d.formatCtx, d.packet)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of file - return what we have
				if len(d.sampleBuffer) > 0 {
					result := make([]float64, len(d.sampleBuffer))
					copy(result, d.sampleBuffer)
					d.sampleBuffer = d.sampleBuffer[:0]
					return result, nil
				}
				return nil, io.EOF
			}
			return nil, fmt.Errorf("failed to read packet: %w", err)
		}
		if ret < 0 {
			return nil, fmt.Errorf("failed to read packet: error code %d", ret)
		}

		// Skip non-audio packets
		if d.packet.StreamIndex() != d.streamIndex {
			ffmpeg.AVPacketUnref(d.packet)
			continue
		}

		// Send packet to decoder
		ret, err = ffmpeg.AVCodecSendPacket(d.codecCtx, d.packet)
		ffmpeg.AVPacketUnref(d.packet)
		if err != nil {
			return nil, fmt.Errorf("failed to send packet to decoder: %w", err)
		}

		// Receive decoded frames
		for {
			ret, err = ffmpeg.AVCodecReceiveFrame(d.codecCtx, d.frame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				return nil, fmt.Errorf("failed to receive frame: %w", err)
			}

			// Extract samples and add to buffer
			samples, err := d.extractSamples()
			if err != nil {
				return nil, fmt.Errorf("failed to extract samples: %w", err)
			}
			d.sampleBuffer = append(d.sampleBuffer, samples...)

			ffmpeg.AVFrameUnref(d.frame)
		}
	}

	// Return requested samples from buffer
	result := make([]float64, numSamples)
	copy(result, d.sampleBuffer[:numSamples])
	d.sampleBuffer = d.sampleBuffer[numSamples:]
	return result, nil
}

// extractSamples extracts float64 samples from the current frame.
// Stereo is automatically downmixed to mono.
func (d *FFmpegDecoder) extractSamples() ([]float64, error) {
	nbSamples := d.frame.NbSamples()
	sampleFormat := d.frame.Format()
	channels := d.channels

	samples := make([]float64, nbSamples)

	// Determine if format is planar (planar formats start at 5)
	isPlanar := sampleFormat >= 5

	if isPlanar && channels == 2 {
		// Planar stereo - separate buffers for left and right
		leftPtr := d.frame.Data().Get(0)
		rightPtr := d.frame.Data().Get(1)
		if leftPtr == nil || rightPtr == nil {
			return nil, fmt.Errorf("missing channel data")
		}

		switch sampleFormat {
		case 6: // AVSampleFmtS16P - planar 16-bit signed
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[:nbSamples*2:nbSamples*2]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[:nbSamples*2:nbSamples*2]
			for i := 0; i < nbSamples; i++ {
				leftVal := int16(leftSlice[i*2]) | int16(leftSlice[i*2+1])<<8
				rightVal := int16(rightSlice[i*2]) | int16(rightSlice[i*2+1])<<8
				samples[i] = (float64(leftVal) + float64(rightVal)) / (2 * 32768.0)
			}

		case 7: // AVSampleFmtS32P - planar 32-bit signed (FLAC)
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[:nbSamples*4:nbSamples*4]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[:nbSamples*4:nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				leftVal := int32(leftSlice[i*4]) |
					int32(leftSlice[i*4+1])<<8 |
					int32(leftSlice[i*4+2])<<16 |
					int32(leftSlice[i*4+3])<<24
				rightVal := int32(rightSlice[i*4]) |
					int32(rightSlice[i*4+1])<<8 |
					int32(rightSlice[i*4+2])<<16 |
					int32(rightSlice[i*4+3])<<24
				samples[i] = (float64(leftVal) + float64(rightVal)) / (2 * 2147483648.0)
			}

		case 8: // AVSampleFmtFltp - planar float
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[:nbSamples*4:nbSamples*4]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[:nbSamples*4:nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				leftBits := uint32(leftSlice[i*4]) |
					uint32(leftSlice[i*4+1])<<8 |
					uint32(leftSlice[i*4+2])<<16 |
					uint32(leftSlice[i*4+3])<<24
				leftFloat := *(*float32)(unsafe.Pointer(&leftBits))

				rightBits := uint32(rightSlice[i*4]) |
					uint32(rightSlice[i*4+1])<<8 |
					uint32(rightSlice[i*4+2])<<16 |
					uint32(rightSlice[i*4+3])<<24
				rightFloat := *(*float32)(unsafe.Pointer(&rightBits))

				samples[i] = float64(leftFloat+rightFloat) / 2
			}

		default:
			return nil, fmt.Errorf("unsupported planar sample format: %d", sampleFormat)
		}
	} else if isPlanar && channels == 1 {
		// Planar mono - just one buffer
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return nil, fmt.Errorf("no data in frame")
		}

		switch sampleFormat {
		case 6: // AVSampleFmtS16P
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*2:nbSamples*2]
			for i := 0; i < nbSamples; i++ {
				val := int16(dataSlice[i*2]) | int16(dataSlice[i*2+1])<<8
				samples[i] = float64(val) / 32768.0
			}

		case 7: // AVSampleFmtS32P
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*4:nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				val := int32(dataSlice[i*4]) |
					int32(dataSlice[i*4+1])<<8 |
					int32(dataSlice[i*4+2])<<16 |
					int32(dataSlice[i*4+3])<<24
				samples[i] = float64(val) / 2147483648.0
			}

		case 8: // AVSampleFmtFltp
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*4:nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				bits := uint32(dataSlice[i*4]) |
					uint32(dataSlice[i*4+1])<<8 |
					uint32(dataSlice[i*4+2])<<16 |
					uint32(dataSlice[i*4+3])<<24
				samples[i] = float64(*(*float32)(unsafe.Pointer(&bits)))
			}

		default:
			return nil, fmt.Errorf("unsupported planar mono format: %d", sampleFormat)
		}
	} else {
		// Interleaved formats
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return nil, fmt.Errorf("no data in frame")
		}

		switch sampleFormat {
		case 1: // AVSampleFmtS16 - interleaved 16-bit signed
			if channels == 1 {
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*2:nbSamples*2]
				for i := 0; i < nbSamples; i++ {
					val := int16(dataSlice[i*2]) | int16(dataSlice[i*2+1])<<8
					samples[i] = float64(val) / 32768.0
				}
			} else {
				// Stereo interleaved: L R L R ...
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*4:nbSamples*4]
				for i := 0; i < nbSamples; i++ {
					leftVal := int16(dataSlice[i*4]) | int16(dataSlice[i*4+1])<<8
					rightVal := int16(dataSlice[i*4+2]) | int16(dataSlice[i*4+3])<<8
					samples[i] = (float64(leftVal) + float64(rightVal)) / (2 * 32768.0)
				}
			}

		case 2: // AVSampleFmtS32 - interleaved 32-bit signed (FLAC)
			if channels == 1 {
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*4:nbSamples*4]
				for i := 0; i < nbSamples; i++ {
					val := int32(dataSlice[i*4]) |
						int32(dataSlice[i*4+1])<<8 |
						int32(dataSlice[i*4+2])<<16 |
						int32(dataSlice[i*4+3])<<24
					samples[i] = float64(val) / 2147483648.0
				}
			} else {
				// Stereo interleaved
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*8:nbSamples*8]
				for i := 0; i < nbSamples; i++ {
					leftVal := int32(dataSlice[i*8]) |
						int32(dataSlice[i*8+1])<<8 |
						int32(dataSlice[i*8+2])<<16 |
						int32(dataSlice[i*8+3])<<24
					rightVal := int32(dataSlice[i*8+4]) |
						int32(dataSlice[i*8+5])<<8 |
						int32(dataSlice[i*8+6])<<16 |
						int32(dataSlice[i*8+7])<<24
					samples[i] = (float64(leftVal) + float64(rightVal)) / (2 * 2147483648.0)
				}
			}

		case 3: // AVSampleFmtFlt - interleaved float
			if channels == 1 {
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*4:nbSamples*4]
				for i := 0; i < nbSamples; i++ {
					bits := uint32(dataSlice[i*4]) |
						uint32(dataSlice[i*4+1])<<8 |
						uint32(dataSlice[i*4+2])<<16 |
						uint32(dataSlice[i*4+3])<<24
					samples[i] = float64(*(*float32)(unsafe.Pointer(&bits)))
				}
			} else {
				// Stereo interleaved
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[:nbSamples*8:nbSamples*8]
				for i := 0; i < nbSamples; i++ {
					leftBits := uint32(dataSlice[i*8]) |
						uint32(dataSlice[i*8+1])<<8 |
						uint32(dataSlice[i*8+2])<<16 |
						uint32(dataSlice[i*8+3])<<24
					rightBits := uint32(dataSlice[i*8+4]) |
						uint32(dataSlice[i*8+5])<<8 |
						uint32(dataSlice[i*8+6])<<16 |
						uint32(dataSlice[i*8+7])<<24
					leftFloat := *(*float32)(unsafe.Pointer(&leftBits))
					rightFloat := *(*float32)(unsafe.Pointer(&rightBits))
					samples[i] = float64(leftFloat+rightFloat) / 2
				}
			}

		default:
			return nil, fmt.Errorf("unsupported interleaved sample format: %d", sampleFormat)
		}
	}

	return samples, nil
}

// SampleRate returns the audio sample rate in Hz.
func (d *FFmpegDecoder) SampleRate() int {
	return d.sampleRate
}

// NumChannels returns the number of audio channels in the source file.
// Note: ReadChunk always returns mono samples (stereo is downmixed).
func (d *FFmpegDecoder) NumChannels() int {
	return d.channels
}

// Close releases all FFmpeg resources.
func (d *FFmpegDecoder) Close() error {
	if d.frame != nil {
		ffmpeg.AVFrameFree(&d.frame)
	}
	if d.packet != nil {
		ffmpeg.AVPacketFree(&d.packet)
	}
	if d.codecCtx != nil {
		ffmpeg.AVCodecFreeContext(&d.codecCtx)
	}
	if d.formatCtx != nil {
		ffmpeg.AVFormatCloseInput(&d.formatCtx)
	}
	return nil
}

// SeekToSample seeks to the specified sample position.
// This enables efficient re-reading for Pass 2 without re-opening the file.
func (d *FFmpegDecoder) SeekToSample(samplePos int64) error {
	// Clear sample buffer
	d.sampleBuffer = d.sampleBuffer[:0]

	// Convert sample position to timestamp
	stream := d.formatCtx.Streams().Get(uintptr(d.streamIndex))
	timeBase := stream.TimeBase()

	// Calculate timestamp: samplePos / sampleRate * timeBase.Den / timeBase.Num
	timestamp := samplePos * int64(timeBase.Den()) / (int64(d.sampleRate) * int64(timeBase.Num()))

	// Seek to timestamp
	ret, err := ffmpeg.AVSeekFrame(d.formatCtx, d.streamIndex, timestamp, ffmpeg.AVSeekFlagBackward)
	if err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to seek: error code %d", ret)
	}

	// Flush codec buffers
	ffmpeg.AVCodecFlushBuffers(d.codecCtx)

	return nil
}
