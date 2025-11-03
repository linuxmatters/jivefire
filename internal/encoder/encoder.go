package encoder

import (
	"errors"
	"fmt"
	"math"
	"unsafe"

	ffmpeg "github.com/csnewman/ffmpeg-go"
)

// Config holds the encoder configuration
type Config struct {
	OutputPath string // Path to output MP4 file
	Width      int    // Video width in pixels
	Height     int    // Video height in pixels
	Framerate  int    // Frames per second
	AudioPath  string // Path to input WAV file (for Phase 2)
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

	// Validate audio input format
	sampleFmt := e.audioDecoder.SampleFmt()
	channels := e.audioDecoder.Channels()
	sampleRate := e.audioDecoder.SampleRate()

	// Log the decoder's output format
	fmt.Printf("Audio input: format=%d, sample_rate=%dHz, channels=%d\n", sampleFmt, sampleRate, channels)

	// Check if we support this sample format
	supportedFormats := map[int32]string{
		1: "16-bit signed integer",
		3: "32-bit float",
		6: "16-bit signed integer planar",
		8: "32-bit float planar",
	}

	sampleFmtInt := int32(sampleFmt)
	if _, ok := supportedFormats[sampleFmtInt]; !ok {
		// Provide helpful format names for common unsupported formats
		formatName := "unknown"
		switch sampleFmtInt {
		case 0:
			formatName = "8-bit unsigned"
		case 2:
			formatName = "32-bit signed integer (24-bit audio)"
		case 4:
			formatName = "64-bit float"
		case 5:
			formatName = "8-bit unsigned planar"
		case 7:
			formatName = "32-bit signed integer planar"
		case 9:
			formatName = "64-bit float planar"
		}
		return fmt.Errorf("unsupported audio format: %s (format %d). Supported formats: 16-bit PCM and 32-bit float", formatName, sampleFmtInt)
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

	// Initialize audio FIFO for frame size adjustment
	e.audioFIFO = NewAudioFIFO()

	// Configure encoder frame with correct size
	e.audioEncFrame.SetNbSamples(e.audioCodec.FrameSize())
	e.audioEncFrame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	e.audioEncFrame.SetChannelLayout(3) // AV_CH_LAYOUT_STEREO
	e.audioEncFrame.SetChannels(2)
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
	// AVSampleFmtFlt = 3 (float interleaved)
	// AVSampleFmtS16P = 6 (signed 16-bit planar)
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

		// For mono planar formats (6, 8), just treat as mono
		case 6: // AVSampleFmtS16P - planar 16-bit (mono)
			dataSlice := (*[1 << 30]byte)(unsafe.Pointer(dataPtr))[: nbSamples*2 : nbSamples*2]
			for i := 0; i < nbSamples; i++ {
				val := int16(dataSlice[i*2]) | int16(dataSlice[i*2+1])<<8
				samples[i] = float32(val) / 32768.0
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
			channels := e.audioDecoder.Channels()
			monoSamples, err := extractFloatsWithDownmix(e.audioDecFrame, channels)
			if err != nil {
				ffmpeg.AVPacketUnref(e.audioPacket)
				return fmt.Errorf("failed to extract samples: %w", err)
			}

			// Convert mono to stereo
			stereoSamples := monoToStereo(monoSamples)

			// Push to FIFO
			e.audioFIFO.Push(stereoSamples)

			// Process all complete frames in FIFO
			for e.audioFIFO.Available() >= encoderFrameSize*2 { // *2 for stereo
				// Pop exactly one encoder frame worth of samples
				frameSamples := e.audioFIFO.Pop(encoderFrameSize * 2)

				// Make frame writable and write samples
				ffmpeg.AVFrameMakeWritable(e.audioEncFrame)
				if err := writeStereoFloats(e.audioEncFrame, frameSamples); err != nil {
					ffmpeg.AVPacketUnref(e.audioPacket)
					return fmt.Errorf("failed to write stereo samples: %w", err)
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
		channels := e.audioDecoder.Channels()
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
