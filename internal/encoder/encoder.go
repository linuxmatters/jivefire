package encoder

import (
	"errors"
	"fmt"

	"github.com/csnewman/ffmpeg-go"
)

// Config holds the encoder configuration
type Config struct {
	OutputPath string // Path to output MP4 file
	Width      int    // Video width in pixels
	Height     int    // Video height in pixels
	Framerate  int    // Frames per second
	AudioPath  string // Path to input WAV file (for Phase 2)
}

// Encoder wraps FFmpeg encoding functionality
type Encoder struct {
	config Config

	// Output muxer (MP4 container)
	formatCtx *ffmpeg.AVFormatContext

	// Video stream and encoder
	videoStream *ffmpeg.AVStream
	videoCodec  *ffmpeg.AVCodecContext

	// Audio stream and encoder (Phase 2)
	audioStream   *ffmpeg.AVStream
	audioCodec    *ffmpeg.AVCodecContext
	audioInputCtx *ffmpeg.AVFormatContext
	audioDecoder  *ffmpeg.AVCodecContext

	// Timestamp tracking
	nextVideoPts int64
	nextAudioPts int64
}

// New creates a new encoder instance
func New(config Config) (*Encoder, error) {
	// Validate configuration
	if config.Width <= 0 || config.Height <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", config.Width, config.Height)
	}
	if config.Framerate <= 0 {
		return nil, fmt.Errorf("invalid framerate: %d", config.Framerate)
	}
	if config.OutputPath == "" {
		return nil, fmt.Errorf("output path cannot be empty")
	}

	return &Encoder{
		config:       config,
		nextVideoPts: 0,
		nextAudioPts: 0,
	}, nil
}

// Initialize sets up the FFmpeg encoder pipeline
func (e *Encoder) Initialize() error {
	var ret int

	// Convert Go string to C string
	outputPath := ffmpeg.ToCStr(e.config.OutputPath)
	defer outputPath.Free()

	// Allocate output format context
	ret, err := ffmpeg.AVFormatAllocOutputContext2(&e.formatCtx, nil, nil, outputPath)
	if err != nil {
		return fmt.Errorf("failed to allocate output context: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %d", ret)
	}

	// Find H.264 encoder
	codec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
	if codec == nil {
		return fmt.Errorf("H.264 encoder not found")
	}

	// Create video stream
	e.videoStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.videoStream == nil {
		return fmt.Errorf("failed to create video stream")
	}
	e.videoStream.SetId(0)

	// Allocate codec context
	e.videoCodec = ffmpeg.AVCodecAllocContext3(codec)
	if e.videoCodec == nil {
		return fmt.Errorf("failed to allocate codec context")
	}

	// Configure video encoder for YouTube compatibility
	e.videoCodec.SetWidth(e.config.Width)
	e.videoCodec.SetHeight(e.config.Height)
	e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtYuv420P) // YouTube-compatible pixel format

	// Set time base (1/framerate)
	timeBase := ffmpeg.AVMakeQ(1, e.config.Framerate)
	e.videoCodec.SetTimeBase(timeBase)

	// Set framerate
	framerate := ffmpeg.AVMakeQ(e.config.Framerate, 1)
	e.videoCodec.SetFramerate(framerate)

	e.videoCodec.SetGopSize(e.config.Framerate * 2) // Keyframe every 2 seconds

	// Set stream timebase
	e.videoStream.SetTimeBase(timeBase)

	// Set encoding options for quality/speed balance (CRF 23 = medium quality)
	// Note: For now, open with nil options. We can add options via av_opt_set later if needed.

	// Open codec
	ret, err = ffmpeg.AVCodecOpen2(e.videoCodec, codec, nil)
	if err != nil {
		return fmt.Errorf("failed to open codec: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to open codec: %d", ret)
	}

	// Copy codec parameters to stream
	ret, err = ffmpeg.AVCodecParametersFromContext(e.videoStream.Codecpar(), e.videoCodec)
	if err != nil {
		return fmt.Errorf("failed to copy codec parameters: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to copy codec parameters: %d", ret)
	}

	// Open output file
	var pb *ffmpeg.AVIOContext
	ret, err = ffmpeg.AVIOOpen(&pb, outputPath, ffmpeg.AVIOFlagWrite)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to open output file: %d", ret)
	}
	e.formatCtx.SetPb(pb)

	// Write file header
	ret, err = ffmpeg.AVFormatWriteHeader(e.formatCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to write header: %d", ret)
	}

	return nil
}

// WriteFrame encodes and writes a single RGB frame
func (e *Encoder) WriteFrame(rgbData []byte) error {
	// Validate frame size
	expectedSize := e.config.Width * e.config.Height * 3 // RGB24 = 3 bytes per pixel
	if len(rgbData) != expectedSize {
		return fmt.Errorf("invalid frame size: got %d, expected %d", len(rgbData), expectedSize)
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	if yuvFrame == nil {
		return fmt.Errorf("failed to allocate YUV frame")
	}
	defer ffmpeg.AVFrameFree(&yuvFrame)

	yuvFrame.SetWidth(e.config.Width)
	yuvFrame.SetHeight(e.config.Height)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))

	ret, err := ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	if err != nil {
		return fmt.Errorf("failed to allocate YUV buffer: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to allocate YUV buffer: %d", ret)
	}

	// Convert RGB to YUV420p
	if err := convertRGBToYUV(rgbData, yuvFrame, e.config.Width, e.config.Height); err != nil {
		return fmt.Errorf("RGB to YUV conversion failed: %w", err)
	}

	// Set presentation timestamp
	yuvFrame.SetPts(e.nextVideoPts)
	e.nextVideoPts++

	// Send frame to encoder
	ret, err = ffmpeg.AVCodecSendFrame(e.videoCodec, yuvFrame)
	if err != nil {
		return fmt.Errorf("failed to send frame to encoder: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to send frame to encoder: %d", ret)
	}

	// Receive and write encoded packets
	for {
		pkt := ffmpeg.AVPacketAlloc()

		ret, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)
		if err != nil || errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
			ffmpeg.AVPacketFree(&pkt)
			break
		}
		if ret < 0 {
			ffmpeg.AVPacketFree(&pkt)
			return fmt.Errorf("failed to receive packet: %d", ret)
		}

		// Set stream index and rescale timestamps
		pkt.SetStreamIndex(e.videoStream.Index())
		ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())

		// Write packet to output
		ret, err = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
		ffmpeg.AVPacketFree(&pkt)

		if err != nil {
			return fmt.Errorf("failed to write packet: %w", err)
		}
		if ret < 0 {
			return fmt.Errorf("failed to write packet: %d", ret)
		}
	}

	return nil
}

// Close finalizes the output file and frees resources
func (e *Encoder) Close() error {
	// Flush encoder
	if e.videoCodec != nil {
		ffmpeg.AVCodecSendFrame(e.videoCodec, nil)

		// Drain remaining packets
		for {
			pkt := ffmpeg.AVPacketAlloc()
			ret, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)

			if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
				ffmpeg.AVPacketFree(&pkt)
				break
			}

			if ret >= 0 {
				pkt.SetStreamIndex(e.videoStream.Index())
				ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())
				ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
			}

			ffmpeg.AVPacketFree(&pkt)
		}
	}

	// Write trailer
	if e.formatCtx != nil {
		ffmpeg.AVWriteTrailer(e.formatCtx)

		// Close output file
		if e.formatCtx.Pb() != nil {
			ffmpeg.AVIOClose(e.formatCtx.Pb())
		}
	}

	// Free codec context
	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}

	// Free format context
	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
