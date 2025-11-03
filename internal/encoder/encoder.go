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
	audioStreamIndex int

	// Timestamp tracking
	nextVideoPts int64
	nextAudioPts int64

	// Audio processing frames/packets
	audioPacket   *ffmpeg.AVPacket
	audioDecFrame *ffmpeg.AVFrame
	audioEncFrame *ffmpeg.AVFrame
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

	// Initialize audio if path provided
	if e.config.AudioPath != "" {
		if err := e.initializeAudio(); err != nil {
			return fmt.Errorf("failed to initialize audio: %w", err)
		}
	}

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

// initializeAudio sets up audio decoder and encoder
func (e *Encoder) initializeAudio() error {
	// Open input audio file
	audioPath := ffmpeg.ToCStr(e.config.AudioPath)
	defer audioPath.Free()
	
	ret, err := ffmpeg.AVFormatOpenInput(&e.audioInputCtx, audioPath, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to open audio input: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to open audio input: %d", ret)
	}

	ret, err = ffmpeg.AVFormatFindStreamInfo(e.audioInputCtx, nil)
	if err != nil {
		return fmt.Errorf("failed to find audio stream info: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to find audio stream info: %d", ret)
	}

	// Find audio stream
	audioStreamIdx := -1
	streams := e.audioInputCtx.Streams()
	for i := uintptr(0); i < uintptr(e.audioInputCtx.NbStreams()); i++ {
		stream := streams.Get(i)
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			audioStreamIdx = int(i)
			break
		}
	}
	if audioStreamIdx == -1 {
		return fmt.Errorf("no audio stream found in input file")
	}

	audioInputStream := streams.Get(uintptr(audioStreamIdx))

	// Set up decoder for input audio
	audioDecoder := ffmpeg.AVCodecFindDecoder(audioInputStream.Codecpar().CodecId())
	if audioDecoder == nil {
		return fmt.Errorf("audio decoder not found")
	}

	e.audioDecoder = ffmpeg.AVCodecAllocContext3(audioDecoder)
	if e.audioDecoder == nil {
		return fmt.Errorf("failed to allocate audio decoder context")
	}

	ret, err = ffmpeg.AVCodecParametersToContext(e.audioDecoder, audioInputStream.Codecpar())
	if err != nil {
		return fmt.Errorf("failed to copy decoder parameters: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to copy decoder parameters: %d", ret)
	}

	ret, err = ffmpeg.AVCodecOpen2(e.audioDecoder, audioDecoder, nil)
	if err != nil {
		return fmt.Errorf("failed to open audio decoder: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to open audio decoder: %d", ret)
	}

	// Set up AAC encoder for output
	audioEncoder := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdAac)
	if audioEncoder == nil {
		return fmt.Errorf("AAC encoder not found")
	}

	e.audioStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.audioStream == nil {
		return fmt.Errorf("failed to create audio stream")
	}
	e.audioStream.SetId(1)

	e.audioCodec = ffmpeg.AVCodecAllocContext3(audioEncoder)
	if e.audioCodec == nil {
		return fmt.Errorf("failed to allocate audio encoder context")
	}

	// Configure AAC encoder
	e.audioCodec.SetSampleFmt(ffmpeg.AVSampleFmtFltp)  // AAC requires float planar
	e.audioCodec.SetSampleRate(e.audioDecoder.SampleRate())
	
	// AAC encoder requires stereo - if input is mono, we'll duplicate channels  
	// Using the older channel layout API
	// 3 = AV_CH_LAYOUT_STEREO (left + right channels)
	e.audioCodec.SetChannelLayout(3)
	e.audioCodec.SetChannels(2)
	
	e.audioCodec.SetBitRate(192000) // 192 kbps
	e.audioStream.SetTimeBase(ffmpeg.AVMakeQ(1, e.audioCodec.SampleRate()))

	ret, err = ffmpeg.AVCodecOpen2(e.audioCodec, audioEncoder, nil)
	if err != nil {
		return fmt.Errorf("failed to open audio encoder: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to open audio encoder: %d", ret)
	}

	ret, err = ffmpeg.AVCodecParametersFromContext(e.audioStream.Codecpar(), e.audioCodec)
	if err != nil {
		return fmt.Errorf("failed to copy audio encoder parameters: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to copy audio encoder parameters: %d", ret)
	}

	// Store the audio stream index
	e.audioStreamIndex = audioStreamIdx

	// Allocate frames and packet for audio processing
	e.audioPacket = ffmpeg.AVPacketAlloc()
	e.audioDecFrame = ffmpeg.AVFrameAlloc()
	e.audioEncFrame = ffmpeg.AVFrameAlloc()

	if e.audioPacket == nil || e.audioDecFrame == nil || e.audioEncFrame == nil {
		return fmt.Errorf("failed to allocate audio frames/packet")
	}

	return nil
}

// WriteFrameRGBA encodes and writes a single RGBA frame
// Converts RGBA (4 bytes/pixel) to RGB24 (3 bytes/pixel) then encodes
func (e *Encoder) WriteFrameRGBA(rgbaData []byte) error {
	// Validate frame size
	expectedSize := e.config.Width * e.config.Height * 4 // RGBA = 4 bytes per pixel
	if len(rgbaData) != expectedSize {
		return fmt.Errorf("invalid RGBA frame size: got %d, expected %d", len(rgbaData), expectedSize)
	}

	// Convert RGBA to RGB24 (strip alpha channel)
	rgb24Size := e.config.Width * e.config.Height * 3
	rgb24Data := make([]byte, rgb24Size)

	srcIdx := 0
	dstIdx := 0
	for dstIdx < rgb24Size {
		rgb24Data[dstIdx] = rgbaData[srcIdx]     // R
		rgb24Data[dstIdx+1] = rgbaData[srcIdx+1] // G
		rgb24Data[dstIdx+2] = rgbaData[srcIdx+2] // B
		// Skip alpha at srcIdx+3
		srcIdx += 4
		dstIdx += 3
	}

	// Use existing RGB24 encoding path
	return e.WriteFrame(rgb24Data)
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

// ProcessAudio reads and processes all audio from the input file
func (e *Encoder) ProcessAudio() error {
	if e.audioInputCtx == nil {
		return errors.New("audio not initialized")
	}

	// Process all audio packets
	for {
		// Read a packet from the input
		ret, err := ffmpeg.AVReadFrame(e.audioInputCtx, e.audioPacket)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of file - flush decoder
				break
			}
			return fmt.Errorf("failed to read audio frame: %w", err)
		}
		if ret < 0 {
			return fmt.Errorf("failed to read audio frame: %d", ret)
		}

		// Only process audio packets
		if e.audioPacket.StreamIndex() != e.audioStreamIndex {
			ffmpeg.AVPacketUnref(e.audioPacket)
			continue
		}

		// Send packet to decoder
		ret, err = ffmpeg.AVCodecSendPacket(e.audioDecoder, e.audioPacket)
		if err != nil {
			ffmpeg.AVPacketUnref(e.audioPacket)
			return fmt.Errorf("failed to send audio packet to decoder: %w", err)
		}

		// Receive decoded frames
		for {
			ret, err = ffmpeg.AVCodecReceiveFrame(e.audioDecoder, e.audioDecFrame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to receive audio frame from decoder: %w", err)
			}

			// Send decoded frame to encoder
			ret, err = ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioDecFrame)
			if err != nil {
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to send audio frame to encoder: %w", err)
			}

			// Receive encoded packets
			for {
				encodedPkt := ffmpeg.AVPacketAlloc()
				ret, err = ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
				if err != nil || errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					ffmpeg.AVPacketFree(&encodedPkt)
					break
				}
				if ret < 0 {
					ffmpeg.AVPacketFree(&encodedPkt)
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to receive audio packet from encoder: %d", ret)
				}

				// Set stream index and timestamps
				encodedPkt.SetStreamIndex(e.audioStream.Index())
				ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

				// Write packet to output
				ret, err = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
				ffmpeg.AVPacketFree(&encodedPkt)
				if err != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to write audio packet: %w", err)
				}
			}
		}

		ffmpeg.AVPacketUnref(e.audioPacket)
	}

	// Flush encoder
	ffmpeg.AVCodecSendPacket(e.audioDecoder, nil)
	
	// Process remaining frames in decoder
	for {
		_, err := ffmpeg.AVCodecReceiveFrame(e.audioDecoder, e.audioDecFrame)
		if err != nil || errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}

		// Send to encoder and process as above
		ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioDecFrame)
		
		for {
			encodedPkt := ffmpeg.AVPacketAlloc()
			_, err := ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
			if err != nil || errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				ffmpeg.AVPacketFree(&encodedPkt)
				break
			}

			encodedPkt.SetStreamIndex(e.audioStream.Index())
			ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())
			ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
			ffmpeg.AVPacketFree(&encodedPkt)
		}
	}

	// Flush encoder
	ffmpeg.AVCodecSendFrame(e.audioCodec, nil)
	
	for {
		encodedPkt := ffmpeg.AVPacketAlloc()
		_, err := ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
		if err != nil || errors.Is(err, ffmpeg.AVErrorEOF) {
			ffmpeg.AVPacketFree(&encodedPkt)
			break
		}

		encodedPkt.SetStreamIndex(e.audioStream.Index())
		ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())
		ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
		ffmpeg.AVPacketFree(&encodedPkt)
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

	// Free codec contexts
	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}
	if e.audioCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.audioCodec)
	}
	if e.audioDecoder != nil {
		ffmpeg.AVCodecFreeContext(&e.audioDecoder)
	}

	// Free audio resources
	if e.audioPacket != nil {
		ffmpeg.AVPacketFree(&e.audioPacket)
	}
	if e.audioDecFrame != nil {
		ffmpeg.AVFrameFree(&e.audioDecFrame)
	}
	if e.audioEncFrame != nil {
		ffmpeg.AVFrameFree(&e.audioEncFrame)
	}
	
	// Close and free audio input context
	if e.audioInputCtx != nil {
		ffmpeg.AVFormatCloseInput(&e.audioInputCtx)
	}

	// Free format context
	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
