package encoder

import (
	"errors"
	"fmt"
	"math"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Config holds the encoder configuration
type Config struct {
	OutputPath    string // Path to output MP4 file
	Width         int    // Video width in pixels
	Height        int    // Video height in pixels
	Framerate     int    // Frames per second
	AudioPath     string // Path to input audio file (legacy mode - decodes internally)
	SampleRate    int    // Audio sample rate for direct sample input (new mode)
	AudioChannels int    // Output audio channels: 1 (mono) or 2 (stereo), defaults to 1
}

// AudioFIFO provides a simple FIFO buffer for audio samples
type AudioFIFO struct {
	buffer []float32
	size   int
}

// NewAudioFIFO creates a new audio FIFO buffer
func NewAudioFIFO() *AudioFIFO {
	return &AudioFIFO{
		buffer: make([]float32, 0, 4096), // Start with reasonable capacity
	}
}

// Push adds samples to the FIFO
func (f *AudioFIFO) Push(samples []float32) {
	f.buffer = append(f.buffer, samples...)
	f.size = len(f.buffer)
}

// Pop removes and returns the requested number of samples
// Returns nil if not enough samples available
func (f *AudioFIFO) Pop(count int) []float32 {
	if f.size < count {
		return nil
	}

	result := make([]float32, count)
	copy(result, f.buffer[:count])

	// Shift remaining samples
	copy(f.buffer, f.buffer[count:])
	f.buffer = f.buffer[:f.size-count]
	f.size -= count

	return result
}

// Available returns the number of samples in the buffer
func (f *AudioFIFO) Available() int {
	return f.size
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
	audioStream      *ffmpeg.AVStream
	audioCodec       *ffmpeg.AVCodecContext
	audioInputCtx    *ffmpeg.AVFormatContext
	audioDecoder     *ffmpeg.AVCodecContext
	audioStreamIndex int

	// Timestamp tracking
	nextVideoPts int64
	nextAudioPts int64

	// Audio processing frames/packets
	audioPacket   *ffmpeg.AVPacket
	audioDecFrame *ffmpeg.AVFrame
	audioEncFrame *ffmpeg.AVFrame

	// Audio resampling (pure Go)
	audioFIFO *AudioFIFO // FIFO for frame size adjustment
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

	// Suppress FFmpeg log output to prevent interference with TUI
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)

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

	// Set encoding options optimized for visualization content via dictionary
	// These are x264-specific private options that must be passed to AVCodecOpen2
	var opts *ffmpeg.AVDictionary
	defer ffmpeg.AVDictFree(&opts)

	// CRF 24 = good quality for busy visualizations
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("crf"), ffmpeg.ToCStr("24"), 0)
	// Faster preset prioritizes encoding speed
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("preset"), ffmpeg.ToCStr("veryfast"), 0)
	// Tune for animation content
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("tune"), ffmpeg.ToCStr("animation"), 0)
	// Main profile for faster encoding and broad compatibility
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
	// Single reference frame (simple vertical bar motion doesn't need multiple refs)
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("ref"), ffmpeg.ToCStr("1"), 0)
	// Reduce b-frames for faster encoding (predictable bar motion)
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("bf"), ffmpeg.ToCStr("1"), 0)
	// Simpler subpixel motion estimation (bars move in discrete pixels)
	ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("subme"), ffmpeg.ToCStr("4"), 0)

	// Open codec with options
	ret, err = ffmpeg.AVCodecOpen2(e.videoCodec, codec, &opts)
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

	// Initialize audio - two modes:
	// 1. AudioPath mode (legacy): opens file and decodes internally
	// 2. SampleRate mode (new): receives pre-decoded samples via WriteAudioSamples
	if e.config.AudioPath != "" {
		if err := e.initializeAudio(); err != nil {
			return fmt.Errorf("failed to initialize audio: %w", err)
		}
	} else if e.config.SampleRate > 0 {
		if err := e.initializeAudioEncoder(); err != nil {
			return fmt.Errorf("failed to initialize audio encoder: %w", err)
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

	// Validate audio input format
	sampleFmt := e.audioDecoder.SampleFmt()
	channels := e.audioDecoder.ChLayout().NbChannels()
	sampleRate := e.audioDecoder.SampleRate()

	// Log the decoder's output format (commented out to avoid TUI interference)
	// fmt.Printf("Audio input: format=%d, sample_rate=%dHz, channels=%d\n", sampleFmt, sampleRate, channels)

	// Check if we support this sample format
	supportedFormats := map[int32]string{
		1: "16-bit signed integer",
		2: "32-bit signed integer", // FLAC uses 32-bit int containers for various bit depths
		3: "32-bit float",
		6: "16-bit signed integer planar",
		7: "32-bit signed integer planar", // FLAC planar
		8: "32-bit float planar",
	}

	sampleFmtInt := int32(sampleFmt)
	if _, ok := supportedFormats[sampleFmtInt]; !ok {
		// Provide helpful format names for common unsupported formats
		formatName := "unknown"
		switch sampleFmtInt {
		case 0:
			formatName = "8-bit unsigned"
		case 4:
			formatName = "64-bit float"
		case 5:
			formatName = "8-bit unsigned planar"
		case 9:
			formatName = "64-bit float planar"
		}
		return fmt.Errorf("unsupported audio format: %s (format %d). Supported formats: 16-bit PCM, 32-bit integer, and 32-bit float", formatName, sampleFmtInt)
	}

	// Check channel count - we support mono and stereo input
	if channels != 1 && channels != 2 {
		return fmt.Errorf("unsupported channel count: %d (only mono and stereo input are supported)", channels)
	}

	// Check sample rate is reasonable
	if sampleRate < 8000 || sampleRate > 192000 {
		return fmt.Errorf("unsupported sample rate: %dHz (must be between 8kHz and 192kHz)", sampleRate)
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
	e.audioCodec.SetSampleFmt(ffmpeg.AVSampleFmtFltp) // AAC requires float planar
	e.audioCodec.SetSampleRate(e.audioDecoder.SampleRate())

	// Set channel configuration based on config (default mono)
	outputChannels := e.config.AudioChannels
	if outputChannels == 0 {
		outputChannels = 1 // Default to mono if not specified
	}

	// Set channel layout using FFmpeg 8.0 API
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 2)
	}

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

	// Initialize audio FIFO for frame size adjustment
	e.audioFIFO = NewAudioFIFO()

	// Configure encoder frame with correct size
	e.audioEncFrame.SetNbSamples(e.audioCodec.FrameSize())
	e.audioEncFrame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 2)
	}
	e.audioEncFrame.SetSampleRate(e.audioCodec.SampleRate())

	ret, err = ffmpeg.AVFrameGetBuffer(e.audioEncFrame, 0)
	if err != nil {
		return fmt.Errorf("failed to allocate encoder frame buffer: %w", err)
	}

	return nil
}

// initializeAudioEncoder sets up the AAC encoder for direct sample input.
// Use this when samples are provided via WriteAudioSamples() instead of from a file.
// Requires SampleRate to be set in Config.
func (e *Encoder) initializeAudioEncoder() error {
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

	// Configure AAC encoder using config sample rate
	e.audioCodec.SetSampleFmt(ffmpeg.AVSampleFmtFltp) // AAC requires float planar
	e.audioCodec.SetSampleRate(e.config.SampleRate)

	// Set channel configuration based on config (default mono)
	outputChannels := e.config.AudioChannels
	if outputChannels == 0 {
		outputChannels = 1 // Default to mono if not specified
	}

	// Set channel layout using FFmpeg 8.0 API
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 2)
	}

	e.audioCodec.SetBitRate(192000) // 192 kbps
	e.audioStream.SetTimeBase(ffmpeg.AVMakeQ(1, e.audioCodec.SampleRate()))

	ret, err := ffmpeg.AVCodecOpen2(e.audioCodec, audioEncoder, nil)
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

	// Allocate encoder frame only (no decoder needed)
	e.audioEncFrame = ffmpeg.AVFrameAlloc()
	if e.audioEncFrame == nil {
		return fmt.Errorf("failed to allocate audio encoder frame")
	}

	// Initialize audio FIFO for frame size adjustment
	e.audioFIFO = NewAudioFIFO()

	// Configure encoder frame with correct size
	e.audioEncFrame.SetNbSamples(e.audioCodec.FrameSize())
	e.audioEncFrame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 2)
	}
	e.audioEncFrame.SetSampleRate(e.audioCodec.SampleRate())

	ret, err = ffmpeg.AVFrameGetBuffer(e.audioEncFrame, 0)
	if err != nil {
		return fmt.Errorf("failed to allocate encoder frame buffer: %w", err)
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

	// Convert RGB to YUV420p using stdlib-optimized implementation
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

// WriteAudioSamples writes pre-decoded audio samples to the encoder.
// Samples should be float32, mono or stereo interleaved depending on AudioChannels config.
// For mono: just the samples. For stereo: L0, R0, L1, R1, ...
// This method handles FIFO buffering and encodes complete AAC frames.
func (e *Encoder) WriteAudioSamples(samples []float32) error {
	if e.audioCodec == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize() // Should be 1024 for AAC
	outputChannels := e.config.AudioChannels
	if outputChannels == 0 {
		outputChannels = 1
	}

	// Push samples to FIFO
	e.audioFIFO.Push(samples)

	// Process all complete frames in FIFO
	samplesPerFrame := encoderFrameSize * outputChannels
	for e.audioFIFO.Available() >= samplesPerFrame {
		// Pop exactly one encoder frame worth of samples
		frameSamples := e.audioFIFO.Pop(samplesPerFrame)

		// Make frame writable and write samples
		ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

		var writeErr error
		if outputChannels == 2 {
			writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
		} else {
			writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write %s samples: %w",
				map[int]string{1: "mono", 2: "stereo"}[outputChannels], writeErr)
		}

		// Set PTS
		e.audioEncFrame.SetPts(e.nextAudioPts)
		e.nextAudioPts += int64(encoderFrameSize)

		// Send to encoder
		ret, err := ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
		if err != nil {
			return fmt.Errorf("failed to send audio frame to encoder: %w", err)
		}
		if ret < 0 {
			return fmt.Errorf("failed to send audio frame to encoder: %d", ret)
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
				return fmt.Errorf("failed to receive audio packet from encoder: %d", ret)
			}

			// Set stream index and timestamps
			encodedPkt.SetStreamIndex(e.audioStream.Index())
			ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

			// Write packet to output
			ret, err = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
			ffmpeg.AVPacketFree(&encodedPkt)
			if err != nil {
				return fmt.Errorf("failed to write audio packet: %w", err)
			}
		}
	}

	return nil
}

// FlushAudioEncoder flushes any remaining samples in the FIFO and encoder.
// Call this after all audio samples have been written.
func (e *Encoder) FlushAudioEncoder() error {
	if e.audioCodec == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize()
	outputChannels := e.config.AudioChannels
	if outputChannels == 0 {
		outputChannels = 1
	}

	// Process any remaining samples in FIFO (may be partial frame)
	samplesPerFrame := encoderFrameSize * outputChannels
	remaining := e.audioFIFO.Available()
	if remaining > 0 {
		// Pad with zeros to make a complete frame
		frameSamples := make([]float32, samplesPerFrame)
		partialSamples := e.audioFIFO.Pop(remaining)
		copy(frameSamples, partialSamples)

		ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

		var writeErr error
		if outputChannels == 2 {
			writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
		} else {
			writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write final samples: %w", writeErr)
		}

		e.audioEncFrame.SetPts(e.nextAudioPts)
		e.nextAudioPts += int64(encoderFrameSize)

		ret, err := ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
		if err != nil {
			return fmt.Errorf("failed to send final audio frame: %w", err)
		}
		if ret < 0 {
			return fmt.Errorf("failed to send final audio frame: %d", ret)
		}
	}

	// Flush encoder by sending NULL frame
	ffmpeg.AVCodecSendFrame(e.audioCodec, nil)

	// Receive all remaining packets
	for {
		encodedPkt := ffmpeg.AVPacketAlloc()
		ret, err := ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
		if err != nil || errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
			ffmpeg.AVPacketFree(&encodedPkt)
			break
		}
		if ret < 0 {
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

// monoToStereo converts mono float32 samples to stereo by duplicating the channel
// monoToStereo duplicates a mono channel into stereo
func monoToStereo(mono []float32) []float32 {
	stereo := make([]float32, len(mono)*2)
	for i := 0; i < len(mono); i++ {
		// Clamp values to prevent NaN/Inf issues
		val := mono[i]
		if math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			val = 0
		} else if val > 1.0 {
			val = 1.0
		} else if val < -1.0 {
			val = -1.0
		}
		stereo[i*2] = val
		stereo[i*2+1] = val
	}
	return stereo
}

// extractFloatsWithDownmix extracts audio samples from a frame and downmixes stereo to mono if needed
func extractFloatsWithDownmix(frame *ffmpeg.AVFrame, channels int) ([]float32, error) {
	// Get frame properties
	nbSamples := frame.NbSamples()
	sampleFormat := frame.Format()

	samples := make([]float32, nbSamples)

	// Check the sample format
	// AVSampleFmtS16 = 1 (signed 16-bit interleaved)
	// AVSampleFmtS32 = 2 (signed 32-bit interleaved, FLAC)
	// AVSampleFmtFlt = 3 (float interleaved)
	// AVSampleFmtS16P = 6 (signed 16-bit planar)
	// AVSampleFmtS32P = 7 (signed 32-bit planar, FLAC)
	// AVSampleFmtFltp = 8 (float planar)

	// Determine if format is planar
	isPlanar := sampleFormat >= 5 // Planar formats start at 5

	if isPlanar && channels == 2 {
		// For planar stereo, we have separate buffers for left and right
		leftPtr := frame.Data().Get(0)
		rightPtr := frame.Data().Get(1)
		if leftPtr == nil || rightPtr == nil {
			return nil, fmt.Errorf("missing channel data")
		}

		switch sampleFormat {
		case 6: // AVSampleFmtS16P - planar 16-bit signed
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[: nbSamples*2 : nbSamples*2]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[: nbSamples*2 : nbSamples*2]
			for i := 0; i < nbSamples; i++ {
				// Read left channel
				leftVal := int16(leftSlice[i*2]) | int16(leftSlice[i*2+1])<<8
				// Read right channel
				rightVal := int16(rightSlice[i*2]) | int16(rightSlice[i*2+1])<<8
				// Average and convert to float32
				samples[i] = (float32(leftVal) + float32(rightVal)) / (2 * 32768.0)
			}

		case 7: // AVSampleFmtS32P - planar 32-bit signed (FLAC)
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[: nbSamples*4 : nbSamples*4]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[: nbSamples*4 : nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				// Read left channel
				leftVal := int32(leftSlice[i*4]) |
					int32(leftSlice[i*4+1])<<8 |
					int32(leftSlice[i*4+2])<<16 |
					int32(leftSlice[i*4+3])<<24
				// Read right channel
				rightVal := int32(rightSlice[i*4]) |
					int32(rightSlice[i*4+1])<<8 |
					int32(rightSlice[i*4+2])<<16 |
					int32(rightSlice[i*4+3])<<24
				// Average and convert to float32
				samples[i] = (float32(leftVal) + float32(rightVal)) / (2 * 2147483648.0)
			}

		case 8: // AVSampleFmtFltp - planar float
			leftSlice := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[: nbSamples*4 : nbSamples*4]
			rightSlice := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[: nbSamples*4 : nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				// Read left channel float
				leftBits := uint32(leftSlice[i*4]) |
					uint32(leftSlice[i*4+1])<<8 |
					uint32(leftSlice[i*4+2])<<16 |
					uint32(leftSlice[i*4+3])<<24
				leftFloat := *(*float32)(unsafe.Pointer(&leftBits))

				// Read right channel float
				rightBits := uint32(rightSlice[i*4]) |
					uint32(rightSlice[i*4+1])<<8 |
					uint32(rightSlice[i*4+2])<<16 |
					uint32(rightSlice[i*4+3])<<24
				rightFloat := *(*float32)(unsafe.Pointer(&rightBits))

				// Average
				samples[i] = (leftFloat + rightFloat) / 2
			}

		default:
			return nil, fmt.Errorf("unsupported planar sample format: %d", sampleFormat)
		}
	} else {
		// Interleaved or mono formats
		dataPtr := frame.Data().Get(0)
		if dataPtr == nil {
			return nil, fmt.Errorf("no data in first channel")
		}

		switch sampleFormat {
		case 1: // AVSampleFmtS16 - interleaved 16-bit signed
			if channels == 1 {
				// Mono
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*2 : nbSamples*2]
				for i := 0; i < nbSamples; i++ {
					val := int16(dataSlice[i*2]) | int16(dataSlice[i*2+1])<<8
					samples[i] = float32(val) / 32768.0
				}
			} else {
				// Stereo interleaved: L R L R L R ...
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*channels*2 : nbSamples*channels*2]
				for i := 0; i < nbSamples; i++ {
					// Read left channel
					leftVal := int16(dataSlice[i*4]) | int16(dataSlice[i*4+1])<<8
					// Read right channel
					rightVal := int16(dataSlice[i*4+2]) | int16(dataSlice[i*4+3])<<8
					// Average and convert
					samples[i] = (float32(leftVal) + float32(rightVal)) / (2 * 32768.0)
				}
			}

		case 2: // AVSampleFmtS32 - interleaved 32-bit signed (FLAC)
			if channels == 1 {
				// Mono
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*4 : nbSamples*4]
				for i := 0; i < nbSamples; i++ {
					val := int32(dataSlice[i*4]) |
						int32(dataSlice[i*4+1])<<8 |
						int32(dataSlice[i*4+2])<<16 |
						int32(dataSlice[i*4+3])<<24
					samples[i] = float32(val) / 2147483648.0 // 2^31
				}
			} else {
				// Stereo interleaved: L R L R L R ...
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*channels*4 : nbSamples*channels*4]
				for i := 0; i < nbSamples; i++ {
					// Read left channel
					leftVal := int32(dataSlice[i*8]) |
						int32(dataSlice[i*8+1])<<8 |
						int32(dataSlice[i*8+2])<<16 |
						int32(dataSlice[i*8+3])<<24
					// Read right channel
					rightVal := int32(dataSlice[i*8+4]) |
						int32(dataSlice[i*8+5])<<8 |
						int32(dataSlice[i*8+6])<<16 |
						int32(dataSlice[i*8+7])<<24
					// Average and convert
					samples[i] = (float32(leftVal) + float32(rightVal)) / (2 * 2147483648.0)
				}
			}

		case 3: // AVSampleFmtFlt - interleaved float
			if channels == 1 {
				// Mono
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*4 : nbSamples*4]
				for i := 0; i < nbSamples; i++ {
					bits := uint32(dataSlice[i*4]) |
						uint32(dataSlice[i*4+1])<<8 |
						uint32(dataSlice[i*4+2])<<16 |
						uint32(dataSlice[i*4+3])<<24
					samples[i] = *(*float32)(unsafe.Pointer(&bits))
				}
			} else {
				// Stereo interleaved
				dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*channels*4 : nbSamples*channels*4]
				for i := 0; i < nbSamples; i++ {
					// Read left channel
					leftBits := uint32(dataSlice[i*8]) |
						uint32(dataSlice[i*8+1])<<8 |
						uint32(dataSlice[i*8+2])<<16 |
						uint32(dataSlice[i*8+3])<<24
					leftFloat := *(*float32)(unsafe.Pointer(&leftBits))

					// Read right channel
					rightBits := uint32(dataSlice[i*8+4]) |
						uint32(dataSlice[i*8+5])<<8 |
						uint32(dataSlice[i*8+6])<<16 |
						uint32(dataSlice[i*8+7])<<24
					rightFloat := *(*float32)(unsafe.Pointer(&rightBits))

					// Average
					samples[i] = (leftFloat + rightFloat) / 2
				}
			}

		// For mono planar formats (6, 7, 8), just treat as mono
		case 6: // AVSampleFmtS16P - planar 16-bit (mono)
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*2 : nbSamples*2]
			for i := 0; i < nbSamples; i++ {
				val := int16(dataSlice[i*2]) | int16(dataSlice[i*2+1])<<8
				samples[i] = float32(val) / 32768.0
			}

		case 7: // AVSampleFmtS32P - planar 32-bit (mono, FLAC)
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*4 : nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				val := int32(dataSlice[i*4]) |
					int32(dataSlice[i*4+1])<<8 |
					int32(dataSlice[i*4+2])<<16 |
					int32(dataSlice[i*4+3])<<24
				samples[i] = float32(val) / 2147483648.0 // 2^31
			}

		case 8: // AVSampleFmtFltp - planar float (mono)
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*4 : nbSamples*4]
			for i := 0; i < nbSamples; i++ {
				bits := uint32(dataSlice[i*4]) |
					uint32(dataSlice[i*4+1])<<8 |
					uint32(dataSlice[i*4+2])<<16 |
					uint32(dataSlice[i*4+3])<<24
				samples[i] = *(*float32)(unsafe.Pointer(&bits))
			}

		default:
			return nil, fmt.Errorf("unsupported sample format: %d", sampleFormat)
		}
	}

	return samples, nil
}

// writeMonoFloats writes mono float samples to an encoder frame
func writeMonoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples)

	// Get pointer for mono channel (planar format)
	dataPtr := frame.Data().Get(0)

	if dataPtr == nil {
		return fmt.Errorf("frame data pointer not allocated")
	}

	// Convert to byte slice for writing
	data := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*4 : nbSamples*4]

	// Write samples to channel
	for i := 0; i < nbSamples; i++ {
		// Write channel - direct float32 byte copy
		sampleFloat := samples[i]
		copy(data[i*4:(i+1)*4], (*[4]byte)(unsafe.Pointer(&sampleFloat))[:])
	}

	return nil
}

// writeStereoFloats writes stereo float samples to an encoder frame
func writeStereoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples) / 2 // stereo has 2 channels

	// Get pointers for both channels (planar format) using .Get() method
	leftPtr := frame.Data().Get(0)
	rightPtr := frame.Data().Get(1)

	if leftPtr == nil || rightPtr == nil {
		return fmt.Errorf("frame data pointers not allocated")
	}

	// Convert to byte slices for writing
	leftData := (*[1 << 30]byte)(unsafe.Pointer(leftPtr))[: nbSamples*4 : nbSamples*4]
	rightData := (*[1 << 30]byte)(unsafe.Pointer(rightPtr))[: nbSamples*4 : nbSamples*4]

	// Write samples to both channels
	for i := 0; i < nbSamples; i++ {
		// Write left channel - direct float32 byte copy
		leftFloat := samples[i*2]
		copy(leftData[i*4:(i+1)*4], (*[4]byte)(unsafe.Pointer(&leftFloat))[:])

		// Write right channel - direct float32 byte copy
		rightFloat := samples[i*2+1]
		copy(rightData[i*4:(i+1)*4], (*[4]byte)(unsafe.Pointer(&rightFloat))[:])
	}

	return nil
}

// ProcessAudioUpToVideoPTS processes audio packets up to (and slightly beyond) the given video PTS
// This allows audio and video to be interleaved during encoding
func (e *Encoder) ProcessAudioUpToVideoPTS(targetVideoPts int64) error {
	if e.audioInputCtx == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize() // Should be 1024 for AAC

	// Calculate target audio PTS based on video PTS
	// Convert from video timebase to audio timebase
	videoTimebaseQ := e.videoStream.TimeBase()
	audioTimebaseQ := e.audioStream.TimeBase()

	targetAudioPts := ffmpeg.AVRescaleQ(targetVideoPts, videoTimebaseQ, audioTimebaseQ)

	// Process audio packets until we reach or exceed the target PTS
	for e.nextAudioPts <= targetAudioPts {
		// Read a packet from the input
		ret, err := ffmpeg.AVReadFrame(e.audioInputCtx, e.audioPacket)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of audio file - we're done for now
				return nil
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

			// Extract float samples from decoded frame (with downmix if stereo)
			channels := e.audioDecoder.ChLayout().NbChannels()
			monoSamples, err := extractFloatsWithDownmix(e.audioDecFrame, channels)
			if err != nil {
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to extract samples: %w", err)
			}

			// Determine output channels
			outputChannels := e.config.AudioChannels
			if outputChannels == 0 {
				outputChannels = 1 // Default to mono
			}

			// Convert to output format
			var outputSamples []float32
			if outputChannels == 2 {
				// Convert mono to stereo
				outputSamples = monoToStereo(monoSamples)
			} else {
				// Keep as mono
				outputSamples = monoSamples
			}

			// Push to FIFO
			e.audioFIFO.Push(outputSamples)

			// Process all complete frames in FIFO
			samplesPerFrame := encoderFrameSize * outputChannels
			for e.audioFIFO.Available() >= samplesPerFrame {
				// Pop exactly one encoder frame worth of samples
				frameSamples := e.audioFIFO.Pop(samplesPerFrame)

				// Make frame writable and write samples
				ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

				var writeErr error
				if outputChannels == 2 {
					writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
				} else {
					writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
				}

				if writeErr != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to write %s samples: %w",
						map[int]string{1: "mono", 2: "stereo"}[outputChannels], writeErr)
				}

				// Set PTS
				e.audioEncFrame.SetPts(e.nextAudioPts)
				e.nextAudioPts += int64(encoderFrameSize)

				// Send to encoder
				ret, err = ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
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
		}

		ffmpeg.AVPacketUnref(e.audioPacket)
	}

	return nil
}

// AudioFlushCallback is called during audio flush with progress updates
type AudioFlushCallback func(packetsProcessed int, elapsed time.Duration)

// FlushRemainingAudio processes any remaining audio packets and flushes the audio encoder
// Call this after all video frames have been written
func (e *Encoder) FlushRemainingAudio(progressCallback AudioFlushCallback) error {
	defer func() {
	}()

	flushStartTime := time.Now()

	if e.audioInputCtx == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize()
	outputChannels := e.config.AudioChannels
	if outputChannels == 0 {
		outputChannels = 1
	}

	// Process remaining audio packets from input file
	audioPacketsRead := 0
	lastProgressUpdate := time.Now()
	for {
		ret, err := ffmpeg.AVReadFrame(e.audioInputCtx, e.audioPacket)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				if progressCallback != nil {
					progressCallback(audioPacketsRead, time.Since(flushStartTime))
				}
				break // Done reading
			}
			return fmt.Errorf("failed to read audio frame: %w", err)
		}
		if ret < 0 {
			return fmt.Errorf("failed to read audio frame: %d", ret)
		}

		if e.audioPacket.StreamIndex() != e.audioStreamIndex {
			ffmpeg.AVPacketUnref(e.audioPacket)
			continue
		}

		audioPacketsRead++
		// Update progress every 20 packets or every 100ms, whichever is less frequent
		now := time.Now()
		if audioPacketsRead%20 == 0 && now.Sub(lastProgressUpdate) >= 100*time.Millisecond {
			if progressCallback != nil {
				progressCallback(audioPacketsRead, time.Since(flushStartTime))
				lastProgressUpdate = now
			}
			// Log less frequently to avoid spam
			if audioPacketsRead%100 == 0 {
			}
		}

		ret, err = ffmpeg.AVCodecSendPacket(e.audioDecoder, e.audioPacket)
		if err != nil {
			ffmpeg.AVPacketUnref(e.audioPacket)
			return fmt.Errorf("failed to send audio packet to decoder: %w", err)
		}

		for {
			ret, err = ffmpeg.AVCodecReceiveFrame(e.audioDecoder, e.audioDecFrame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to receive audio frame from decoder: %w", err)
			}

			channels := e.audioDecoder.ChLayout().NbChannels()
			monoSamples, err := extractFloatsWithDownmix(e.audioDecFrame, channels)
			if err != nil {
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to extract samples: %w", err)
			}

			var outputSamples []float32
			if outputChannels == 2 {
				outputSamples = monoToStereo(monoSamples)
			} else {
				outputSamples = monoSamples
			}

			e.audioFIFO.Push(outputSamples)

			samplesPerFrame := encoderFrameSize * outputChannels
			for e.audioFIFO.Available() >= samplesPerFrame {
				frameSamples := e.audioFIFO.Pop(samplesPerFrame)
				ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

				var writeErr error
				if outputChannels == 2 {
					writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
				} else {
					writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
				}

				if writeErr != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to write samples: %w", writeErr)
				}

				e.audioEncFrame.SetPts(e.nextAudioPts)
				e.nextAudioPts += int64(encoderFrameSize)

				ret, err = ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
				if err != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to send audio frame to encoder: %w", err)
				}

				for {
					encodedPkt := ffmpeg.AVPacketAlloc()
					ret, err = ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
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
		}

		ffmpeg.AVPacketUnref(e.audioPacket)
	}

	// Flush audio decoder
	ffmpeg.AVCodecSendPacket(e.audioDecoder, nil)
	for {
		_, err := ffmpeg.AVCodecReceiveFrame(e.audioDecoder, e.audioDecFrame)
		if err != nil || errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}

		channels := e.audioDecoder.ChLayout().NbChannels()
		monoSamples, _ := extractFloatsWithDownmix(e.audioDecFrame, channels)
		var outputSamples []float32
		if outputChannels == 2 {
			outputSamples = monoToStereo(monoSamples)
		} else {
			outputSamples = monoSamples
		}
		e.audioFIFO.Push(outputSamples)

		samplesPerFrame := encoderFrameSize * outputChannels
		for e.audioFIFO.Available() >= samplesPerFrame {
			frameSamples := e.audioFIFO.Pop(samplesPerFrame)
			ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

			if outputChannels == 2 {
				writeStereoFloats(e.audioEncFrame, frameSamples)
			} else {
				writeMonoFloats(e.audioEncFrame, frameSamples)
			}

			e.audioEncFrame.SetPts(e.nextAudioPts)
			e.nextAudioPts += int64(encoderFrameSize)
			ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)

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
		}
	}

	// Handle remaining samples in FIFO (pad with silence if needed)
	if e.audioFIFO.Available() > 0 {
		remaining := e.audioFIFO.Available()
		needed := encoderFrameSize * outputChannels

		partialSamples := e.audioFIFO.Pop(remaining)
		paddedSamples := make([]float32, needed)
		copy(paddedSamples, partialSamples)

		ffmpeg.AVFrameMakeWritable(e.audioEncFrame)
		if outputChannels == 2 {
			writeStereoFloats(e.audioEncFrame, paddedSamples)
		} else {
			writeMonoFloats(e.audioEncFrame, paddedSamples)
		}

		e.audioEncFrame.SetPts(e.nextAudioPts)
		ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)

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
	}

	// Final flush of audio encoder
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

// ProcessAudio reads and processes all audio from the input file
func (e *Encoder) ProcessAudio() error {
	if e.audioInputCtx == nil {
		return errors.New("audio not initialized")
	}

	encoderFrameSize := e.audioCodec.FrameSize() // Should be 1024 for AAC

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

			// Extract float samples from decoded frame (with downmix if stereo)
			channels := e.audioDecoder.ChLayout().NbChannels()
			monoSamples, err := extractFloatsWithDownmix(e.audioDecFrame, channels)
			if err != nil {
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to extract samples: %w", err)
			}

			// Determine output channels
			outputChannels := e.config.AudioChannels
			if outputChannels == 0 {
				outputChannels = 1 // Default to mono
			}

			// Convert to output format
			var outputSamples []float32
			if outputChannels == 2 {
				// Convert mono to stereo
				outputSamples = monoToStereo(monoSamples)
			} else {
				// Keep as mono
				outputSamples = monoSamples
			}

			// Push to FIFO
			e.audioFIFO.Push(outputSamples)

			// Process all complete frames in FIFO
			samplesPerFrame := encoderFrameSize * outputChannels
			for e.audioFIFO.Available() >= samplesPerFrame {
				// Pop exactly one encoder frame worth of samples
				frameSamples := e.audioFIFO.Pop(samplesPerFrame)

				// Make frame writable and write samples
				ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

				var writeErr error
				if outputChannels == 2 {
					writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
				} else {
					writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
				}

				if writeErr != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to write %s samples: %w",
						map[int]string{1: "mono", 2: "stereo"}[outputChannels], writeErr)
				}

				// Set PTS
				e.audioEncFrame.SetPts(e.nextAudioPts)
				e.nextAudioPts += int64(encoderFrameSize)

				// Send to encoder
				ret, err = ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
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
		}

		ffmpeg.AVPacketUnref(e.audioPacket)
	}

	// Flush decoder
	ffmpeg.AVCodecSendPacket(e.audioDecoder, nil)

	// Process remaining frames in decoder
	for {
		_, err := ffmpeg.AVCodecReceiveFrame(e.audioDecoder, e.audioDecFrame)
		if err != nil || errors.Is(err, ffmpeg.AVErrorEOF) {
			break
		}

		// Extract and process same as above
		channels := e.audioDecoder.ChLayout().NbChannels()
		monoSamples, _ := extractFloatsWithDownmix(e.audioDecFrame, channels)
		stereoSamples := monoToStereo(monoSamples)
		e.audioFIFO.Push(stereoSamples)

		// Process all complete frames in FIFO (same as above)
		for e.audioFIFO.Available() >= encoderFrameSize*2 {
			frameSamples := e.audioFIFO.Pop(encoderFrameSize * 2)

			ffmpeg.AVFrameMakeWritable(e.audioEncFrame)
			writeStereoFloats(e.audioEncFrame, frameSamples)

			e.audioEncFrame.SetPts(e.nextAudioPts)
			e.nextAudioPts += int64(encoderFrameSize)

			ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)

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
	}

	// Process any remaining samples in FIFO (pad with silence if needed)
	if e.audioFIFO.Available() > 0 {
		remaining := e.audioFIFO.Available()
		needed := encoderFrameSize * 2

		// Pop what we have
		partialSamples := e.audioFIFO.Pop(remaining)

		// Pad with silence to complete frame
		paddedSamples := make([]float32, needed)
		copy(paddedSamples, partialSamples)
		// Rest is already zero (silence)

		ffmpeg.AVFrameMakeWritable(e.audioEncFrame)
		writeStereoFloats(e.audioEncFrame, paddedSamples)

		e.audioEncFrame.SetPts(e.nextAudioPts)
		ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)

		// Flush encoder
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
	}

	// Final flush
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

// GetVideoFramesEncoded returns the number of video frames encoded so far
func (e *Encoder) GetVideoFramesEncoded() int64 {
	return e.nextVideoPts
}
