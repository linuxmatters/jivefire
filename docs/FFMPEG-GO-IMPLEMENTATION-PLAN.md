# ffmpeg-go Integration Implementation Plan

**Date:** 3 November 2025
**Project:** Jivefire Audio Visualizer
**Objective:** Replace external FFmpeg process with embedded ffmpeg-go library
**Version:** 2.0 - Revised for 2-Pass Streaming Architecture

---

## ⚠️ Important: Architecture Alignment

**This plan has been revised** to align with Jivefire's current 2-pass streaming architecture implemented in commit `b0141a382ef45488121c74dbc82e94180d02de98`.

### Current Jivefire Architecture (As of Nov 2025)

Jivefire uses a sophisticated **2-pass streaming architecture** with a **sliding buffer** for continuous audio/video synchronization:

**Pass 1: Audio Analysis** (`internal/audio/analyzer.go`)
```go
// Streams through audio with sliding buffer
profile, err := audio.AnalyzeAudio(inputFile)
// Returns: OptimalBaseScale, GlobalPeak, GlobalRMS, NumFrames
```

**Pass 2: Video Rendering** (`cmd/jivefire/main.go`)
```go
// Uses profile from Pass 1, streams through audio again
samplesPerFrame := config.SampleRate / config.FPS
fftBuffer := make([]float64, config.FFTSize)

// Pre-fill buffer
initialChunk := reader.ReadChunk(config.FFTSize)
copy(fftBuffer, initialChunk)

for frameNum := 0; frameNum < numFrames; frameNum++ {
    // Use current buffer for FFT
    chunk := fftBuffer[:config.FFTSize]

    // Process → Render → Write to FFmpeg
    coeffs := processor.ProcessChunk(chunk)
    barHeights := audio.BinFFT(coeffs, sensitivity, profile.OptimalBaseScale)
    frameData := frame.Draw(barHeights, ...)
    stdin.Write(frameData) // Currently writes to FFmpeg pipe

    // CRITICAL: Advance buffer by samplesPerFrame (not FFTSize!)
    newSamples := reader.ReadChunk(samplesPerFrame)
    copy(fftBuffer, fftBuffer[samplesPerFrame:])
    copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
}
```

**Why This Matters for ffmpeg-go Integration:**

1. ✅ **Pass 1 stays unchanged** - analysis doesn't involve FFmpeg
2. ⚠️ **Pass 2 must preserve sliding buffer** - this is how audio/video stay in sync
3. ⚠️ **Audio handling is different** - current FFmpeg reads WAV directly as `-i` input
4. ⚠️ **No sample re-encoding needed** - audio stream just needs to be copied to output

### Key Changes from Original Plan

| Aspect | Original Plan (Incorrect) | Revised Plan (Correct) |
|--------|---------------------------|------------------------|
| **Architecture** | Single-pass with parallel audio encoding | 2-pass: analysis → rendering |
| **Audio Handling** | Read samples, encode to AAC in goroutine | Demux WAV file, copy audio stream |
| **Sync Mechanism** | Manual PTS management for A/V | Sliding buffer with fixed samplesPerFrame |
| **Frame Loop** | Simple `for { WriteFrame() }` | Must preserve sliding buffer pattern |
| **AudioProfile** | Not mentioned | Critical - provides OptimalBaseScale |

---

## Overview

This document provides a detailed, step-by-step implementation plan for integrating [csnewman/ffmpeg-go](https://github.com/csnewman/ffmpeg-go) into Jivefire while **preserving the 2-pass streaming architecture** that enables memory-efficient processing and perfect audio/video synchronization.

The migration will transform Jivefire from a tool requiring external FFmpeg installation to a fully standalone binary with embedded video encoding capabilities.

**Estimated Total Time:** 2.5 days (simplified due to no audio re-encoding)
**Risk Level:** Low (clear rollback path, phased approach)
**Complexity:** Medium (FFmpeg C API requires careful handling, must preserve sync)---

## Prerequisites

### Development Environment

**Required:**
- Go 1.21+ (current Jivefire requirement)
- C compiler (gcc/clang) for CGO compilation
- FFmpeg development headers (for reference/testing)
- Git for version control

**Recommended:**
- `testdata/dream.wav` (existing test file)
- FFmpeg CLI tool (for output validation)
- VLC or mpv (for video playback verification)

### Knowledge Requirements

**Essential:**
- Go programming fundamentals
- Basic FFmpeg concepts (codecs, containers, pixel formats)
- Understanding of Jivefire's current architecture

**Helpful but not required:**
- C programming basics (for debugging CGO issues)
- FFmpeg C API experience (examples provided in plan)

---

## Phase 1: Proof of Concept (Day 1, ~6-8 hours)

**Goal:** Verify ffmpeg-go works in Jivefire's environment with minimal changes

### Step 1.1: Add ffmpeg-go Dependency (30 minutes)

**What:** Add ffmpeg-go to `go.mod` and verify CGO compilation

**Actions:**
```bash
cd /home/martin/Development/linuxmatters/jivefire
go get github.com/csnewman/ffmpeg-go@v0.6.0
go mod tidy
```

**Verification:**
```bash
# Should complete without errors
go build ./cmd/jivefire
```

**Troubleshooting:**
- If CGO errors occur, ensure `gcc` is installed: `which gcc`
- If linking errors, check system libraries: `ldconfig -p | grep libm`
- On NixOS, may need `nix develop` shell with gcc

**Success Criteria:**
- ✅ `go.mod` contains `github.com/csnewman/ffmpeg-go v0.6.0`
- ✅ `go build` completes successfully
- ✅ Binary size increases by ~60-70MB (expected)

---

### Step 1.2: Create Encoder Package Structure (1 hour)

**What:** Establish new internal package for video encoding logic

**Actions:**

1. **Create directory:**
```bash
mkdir -p internal/encoder
```

2. **Create `internal/encoder/encoder.go`:**
```go
package encoder

import (
	"fmt"
	"github.com/csnewman/ffmpeg-go"
)

// Config holds video encoding parameters
type Config struct {
	OutputPath string
	Width      int
	Height     int
	Framerate  int
	AudioPath  string
}

// Encoder manages video encoding state
type Encoder struct {
	config      Config
	formatCtx   *ffmpeg.AVFormatContext
	videoStream *ffmpeg.AVStream
	audioStream *ffmpeg.AVStream
	videoCodec  *ffmpeg.AVCodecContext
	audioCodec  *ffmpeg.AVCodecContext
}

// New creates a new encoder instance
func New(config Config) (*Encoder, error) {
	return &Encoder{config: config}, nil
}

// Close releases all FFmpeg resources
func (e *Encoder) Close() error {
	// Will implement in Step 1.3
	return nil
}
```

3. **Create `internal/encoder/encoder_test.go`:**
```go
package encoder

import "testing"

func TestNew(t *testing.T) {
	config := Config{
		OutputPath: "test.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
		AudioPath:  "test.wav",
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	if enc == nil {
		t.Fatal("New() returned nil encoder")
	}

	defer enc.Close()
}
```

**Verification:**
```bash
go test ./internal/encoder
```

**Success Criteria:**
- ✅ Package structure matches Jivefire conventions
- ✅ Basic test passes
- ✅ No compilation errors

---

### Step 1.3: Initialize Video Encoder (2-3 hours)

**What:** Set up H.264 video encoder with proper parameters

**Actions:**

1. **Implement `Initialize()` method in `encoder.go`:**

```go
// Initialize sets up the muxer and video encoder
func (e *Encoder) Initialize() error {
	// Step 1: Allocate output format context
	e.formatCtx = ffmpeg.AVFormatAllocContext()
	if e.formatCtx == nil {
		return fmt.Errorf("failed to allocate format context")
	}

	// Step 2: Guess output format from filename
	outputFormat := ffmpeg.AVGuessFormat(nil, ffmpeg.AllocCStr(e.config.OutputPath), nil)
	if outputFormat == nil {
		return fmt.Errorf("failed to determine output format")
	}
	e.formatCtx.SetOformat(outputFormat)

	// Step 3: Open output file
	var ioCtx *ffmpeg.AVIOContext
	ret := ffmpeg.AVIOOpen(&ioCtx, ffmpeg.AllocCStr(e.config.OutputPath), ffmpeg.AVIOFlagWrite)
	if ret < 0 {
		return fmt.Errorf("failed to open output file: %d", ret)
	}
	e.formatCtx.SetPb(ioCtx)

	// Step 4: Find H.264 encoder
	codec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
	if codec == nil {
		return fmt.Errorf("H.264 encoder not found")
	}

	// Step 5: Create video stream
	e.videoStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.videoStream == nil {
		return fmt.Errorf("failed to create video stream")
	}
	e.videoStream.SetId(0)

	// Step 6: Allocate codec context
	e.videoCodec = ffmpeg.AVCodecAllocContext3(codec)
	if e.videoCodec == nil {
		return fmt.Errorf("failed to allocate codec context")
	}

	// Step 7: Configure video parameters
	e.videoCodec.SetWidth(int32(e.config.Width))
	e.videoCodec.SetHeight(int32(e.config.Height))
	e.videoCodec.SetTimeBase(ffmpeg.AVRational{Num: 1, Den: int32(e.config.Framerate)})
	e.videoCodec.SetFramerate(ffmpeg.AVRational{Num: int32(e.config.Framerate), Den: 1})
	e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtYuv420p)

	// H.264-specific options for YouTube compatibility
	e.videoCodec.SetGopSize(int32(e.config.Framerate * 2)) // 2-second GOP
	e.videoCodec.SetMaxBFrames(2)

	// Step 8: Set container stream parameters
	e.videoStream.SetTimeBase(e.videoCodec.TimeBase())

	// Step 9: Open codec
	opts := ffmpeg.AVDictAllocate()
	ffmpeg.AVDictSet(&opts, ffmpeg.AllocCStr("preset"), ffmpeg.AllocCStr("medium"), 0)
	ffmpeg.AVDictSet(&opts, ffmpeg.AllocCStr("crf"), ffmpeg.AllocCStr("23"), 0)

	ret = ffmpeg.AVCodecOpen2(e.videoCodec, codec, &opts)
	ffmpeg.AVDictFree(&opts)
	if ret < 0 {
		return fmt.Errorf("failed to open video codec: %d", ret)
	}

	// Step 10: Copy codec parameters to stream
	ret = ffmpeg.AVCodecParametersFromContext(e.videoStream.Codecpar(), e.videoCodec)
	if ret < 0 {
		return fmt.Errorf("failed to copy codec parameters: %d", ret)
	}

	// Step 11: Write header
	ret = ffmpeg.AVFormatWriteHeader(e.formatCtx, nil)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %d", ret)
	}

	return nil
}

// Close releases all FFmpeg resources
func (e *Encoder) Close() error {
	// Write trailer before cleanup
	if e.formatCtx != nil {
		ffmpeg.AVWriteTrailer(e.formatCtx)

		if e.formatCtx.Pb() != nil {
			ffmpeg.AVIOClosep(&e.formatCtx.Pb())
		}
	}

	// Free codec contexts
	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}
	if e.audioCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.audioCodec)
	}

	// Free format context
	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
```

2. **Add initialization test:**

```go
func TestInitialize(t *testing.T) {
	config := Config{
		OutputPath: "testdata/poc-video.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
		AudioPath:  "testdata/dream.wav",
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	// Verify encoder was created
	if enc.videoCodec == nil {
		t.Fatal("video codec not initialized")
	}
	if enc.formatCtx == nil {
		t.Fatal("format context not initialized")
	}
}
```

**Verification:**
```bash
go test -v ./internal/encoder -run TestInitialize
```

**Common Issues:**

| Issue | Cause | Solution |
|-------|-------|----------|
| `H.264 encoder not found` | Static library missing x264 | Verify ffmpeg-go installation |
| `failed to open output file` | Permissions or invalid path | Check directory exists and is writable |
| `failed to allocate` errors | Memory issues | Check system resources |

**Success Criteria:**
- ✅ Test creates valid MP4 file header
- ✅ `ffprobe testdata/poc-video.mp4` shows H.264 stream
- ✅ No memory leaks (can verify with `valgrind` if needed)

---

### Step 1.4: Implement Single Frame Encoding (2-3 hours)

**What:** Encode one static test frame to verify the full pipeline

**Actions:**

1. **Add frame encoding method to `encoder.go`:**

```go
// WriteFrame encodes a single RGB24 frame
func (e *Encoder) WriteFrame(rgbData []byte, pts int64) error {
	if len(rgbData) != e.config.Width*e.config.Height*3 {
		return fmt.Errorf("invalid RGB data size: got %d, want %d",
			len(rgbData), e.config.Width*e.config.Height*3)
	}

	// Step 1: Allocate frame for RGB24 input
	rgbFrame := ffmpeg.AVFrameAlloc()
	if rgbFrame == nil {
		return fmt.Errorf("failed to allocate RGB frame")
	}
	defer ffmpeg.AVFrameFree(&rgbFrame)

	rgbFrame.SetWidth(int32(e.config.Width))
	rgbFrame.SetHeight(int32(e.config.Height))
	rgbFrame.SetFormat(int32(ffmpeg.AVPixFmtRgb24))

	ret := ffmpeg.AVFrameGetBuffer(rgbFrame, 0)
	if ret < 0 {
		return fmt.Errorf("failed to allocate RGB frame buffer: %d", ret)
	}

	// Step 2: Copy RGB data into frame
	linesize := rgbFrame.Linesize()[0]
	dataPtr := rgbFrame.Data()[0]
	for y := 0; y < e.config.Height; y++ {
		srcOffset := y * e.config.Width * 3
		dstOffset := y * int(linesize)
		copy(dataPtr[dstOffset:dstOffset+e.config.Width*3], rgbData[srcOffset:srcOffset+e.config.Width*3])
	}

	// Step 3: Allocate frame for YUV420p output
	yuvFrame := ffmpeg.AVFrameAlloc()
	if yuvFrame == nil {
		return fmt.Errorf("failed to allocate YUV frame")
	}
	defer ffmpeg.AVFrameFree(&yuvFrame)

	yuvFrame.SetWidth(int32(e.config.Width))
	yuvFrame.SetHeight(int32(e.config.Height))
	yuvFrame.SetFormat(int32(ffmpeg.AVPixFmtYuv420p))
	yuvFrame.SetPts(pts)

	ret = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	if ret < 0 {
		return fmt.Errorf("failed to allocate YUV frame buffer: %d", ret)
	}

	// Step 4: Convert RGB24 -> YUV420p using swscale
	swsCtx := ffmpeg.SwsGetContext(
		int32(e.config.Width), int32(e.config.Height), ffmpeg.AVPixFmtRgb24,
		int32(e.config.Width), int32(e.config.Height), ffmpeg.AVPixFmtYuv420p,
		ffmpeg.SwsFlags(ffmpeg.SwsBilinear), nil, nil, nil,
	)
	if swsCtx == nil {
		return fmt.Errorf("failed to create swscale context")
	}
	defer ffmpeg.SwsFreeContext(swsCtx)

	ffmpeg.SwsScale(
		swsCtx,
		rgbFrame.Data(), rgbFrame.Linesize(), 0, int32(e.config.Height),
		yuvFrame.Data(), yuvFrame.Linesize(),
	)

	// Step 5: Send frame to encoder
	ret = ffmpeg.AVCodecSendFrame(e.videoCodec, yuvFrame)
	if ret < 0 {
		return fmt.Errorf("failed to send frame to encoder: %d", ret)
	}

	// Step 6: Receive encoded packets
	for {
		pkt := ffmpeg.AVPacketAlloc()
		defer ffmpeg.AVPacketFree(&pkt)

		ret := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)
		if ret == ffmpeg.AVERROR_EAGAIN || ret == ffmpeg.AVERROR_EOF {
			break
		}
		if ret < 0 {
			return fmt.Errorf("failed to receive packet: %d", ret)
		}

		// Step 7: Rescale packet timestamps
		pkt.SetStreamIndex(e.videoStream.Index())
		ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())

		// Step 8: Write packet to file
		ret = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
		if ret < 0 {
			return fmt.Errorf("failed to write packet: %d", ret)
		}
	}

	return nil
}
```

2. **Add test with single frame:**

```go
func TestWriteSingleFrame(t *testing.T) {
	config := Config{
		OutputPath: "testdata/poc-single-frame.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	// Create solid red frame (for easy visual verification)
	frame := make([]byte, 1280*720*3)
	for i := 0; i < len(frame); i += 3 {
		frame[i] = 255   // R
		frame[i+1] = 0   // G
		frame[i+2] = 0   // B
	}

	if err := enc.WriteFrame(frame, 0); err != nil {
		t.Fatalf("WriteFrame() failed: %v", err)
	}
}
```

**Verification:**

```bash
# Run test
go test -v ./internal/encoder -run TestWriteSingleFrame

# Verify output
ffprobe testdata/poc-single-frame.mp4 2>&1 | grep -E '(Stream|Duration)'
# Should show: Video: h264, yuv420p, 1280x720

# Visual check (should show red frame)
mpv testdata/poc-single-frame.mp4
```

**Success Criteria:**
- ✅ Test passes without errors
- ✅ Output file exists and is playable
- ✅ Video shows red frame (confirms RGB→YUV conversion)
- ✅ `ffprobe` shows correct codec, resolution, pixel format

---

### Step 1.5: POC Integration Test (1-2 hours)

**What:** Create end-to-end test using actual Jivefire data

**Actions:**

1. **Create `cmd/poc/main.go` (temporary testing tool):**

```go
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/linuxmatters/jivefire/internal/encoder"
	"github.com/linuxmatters/jivefire/internal/renderer"
)

func main() {
	// Use existing renderer to generate a few test frames
	bgImage, err := renderer.LoadBackground("assets/bg.png")
	if err != nil {
		log.Fatalf("Failed to load background: %v", err)
	}

	config := encoder.Config{
		OutputPath: "poc-output.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
	}

	enc, err := encoder.New(config)
	if err != nil {
		log.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		log.Fatalf("Failed to initialize encoder: %v", err)
	}

	// Generate 90 frames (3 seconds at 30fps)
	for i := 0; i < 90; i++ {
		// Create frame data (reuse renderer's DrawFrame)
		frame := renderer.DrawFrame(bgImage, 1280, 720, nil) // Placeholder bars

		if err := enc.WriteFrame(frame, int64(i)); err != nil {
			log.Fatalf("Failed to write frame %d: %v", i, err)
		}

		if (i+1)%30 == 0 {
			fmt.Printf("Encoded %d frames...\n", i+1)
		}
	}

	fmt.Println("POC complete: poc-output.mp4")
}
```

2. **Run POC:**

```bash
go run ./cmd/poc
mpv poc-output.mp4
```

**Success Criteria:**
- ✅ POC generates 3-second video without errors
- ✅ Video plays smoothly at 30fps
- ✅ Background and bars visible (confirms renderer integration)
- ✅ File size reasonable (~100-500KB for 3s test)

---

### Step 1.6: Phase 1 Validation (30 minutes)

**Checklist:**

- [ ] ffmpeg-go dependency added and compiles
- [ ] `internal/encoder` package structure complete
- [ ] Video encoder initialization working
- [ ] Single frame encoding successful
- [ ] POC integration test generates playable video
- [ ] All tests pass: `go test ./...`
- [ ] Binary size increased by expected amount

**Rollback Plan:**
If major issues found, remove ffmpeg-go and revert:
```bash
git checkout HEAD -- go.mod go.sum
rm -rf internal/encoder cmd/poc
go mod tidy
```

**Decision Point:**
✅ **Proceed to Phase 2** if all validation criteria met
❌ **Stop and debug** if encoder initialization or frame writing fails

---

## Phase 2: Audio Stream Integration (Day 2, ~4-5 hours)

**Goal:** Add audio stream to output by demuxing input WAV file (no re-encoding needed)

**Key Insight:** Jivefire's current architecture passes the WAV file to FFmpeg as `-i <audio.wav>`, which demuxes and encodes it. With ffmpeg-go, we'll do the same: open the WAV file as an input, demux it, encode to AAC, and mux with video.

### Step 2.1: Add Audio Input Demuxer (2-3 hours)

**What:** Open WAV file with ffmpeg-go demuxer to read audio stream

**Actions:**

1. **Extend `Encoder` struct to include audio input:**

```go
type Encoder struct {
	config        Config

	// Output muxer
	formatCtx     *ffmpeg.AVFormatContext
	videoStream   *ffmpeg.AVStream
	audioStream   *ffmpeg.AVStream

	// Video encoder
	videoCodec    *ffmpeg.AVCodecContext

	// Audio input/output
	audioInputCtx *ffmpeg.AVFormatContext  // Input WAV demuxer
	audioCodec    *ffmpeg.AVCodecContext   // AAC encoder
	audioDecoder  *ffmpeg.AVCodecContext   // WAV decoder

	// Timestamp tracking
	nextVideoPts  int64
	nextAudioPts  int64
}
```

2. **Add audio initialization to `Initialize()` in `encoder.go`:**

```go
// After video encoder setup, before WriteHeader

// Step 1: Open input audio file
var audioInputCtx *ffmpeg.AVFormatContext
ret := ffmpeg.AVFormatOpenInput(&audioInputCtx, ffmpeg.AllocCStr(e.config.AudioPath), nil, nil)
if ret < 0 {
	return fmt.Errorf("failed to open audio input: %d", ret)
}
e.audioInputCtx = audioInputCtx

ret = ffmpeg.AVFormatFindStreamInfo(audioInputCtx, nil)
if ret < 0 {
	return fmt.Errorf("failed to find audio stream info: %d", ret)
}

// Step 2: Find audio stream in input
audioStreamIdx := -1
for i := 0; i < int(audioInputCtx.NbStreams()); i++ {
	stream := audioInputCtx.Streams()[i]
	if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
		audioStreamIdx = i
		break
	}
}
if audioStreamIdx == -1 {
	return fmt.Errorf("no audio stream found in input file")
}

audioInputStream := audioInputCtx.Streams()[audioStreamIdx]

// Step 3: Set up decoder for input audio (WAV)
audioDecoder := ffmpeg.AVCodecFindDecoder(audioInputStream.Codecpar().CodecId())
if audioDecoder == nil {
	return fmt.Errorf("audio decoder not found")
}

e.audioDecoder = ffmpeg.AVCodecAllocContext3(audioDecoder)
if e.audioDecoder == nil {
	return fmt.Errorf("failed to allocate audio decoder context")
}

ret = ffmpeg.AVCodecParametersToContext(e.audioDecoder, audioInputStream.Codecpar())
if ret < 0 {
	return fmt.Errorf("failed to copy decoder parameters: %d", ret)
}

ret = ffmpeg.AVCodecOpen2(e.audioDecoder, audioDecoder, nil)
if ret < 0 {
	return fmt.Errorf("failed to open audio decoder: %d", ret)
}

// Step 4: Set up AAC encoder for output
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

// Configure AAC encoder to match input audio properties
e.audioCodec.SetSampleFmt(ffmpeg.AVSampleFmtFltp)  // AAC requires float planar
e.audioCodec.SetSampleRate(e.audioDecoder.SampleRate())
e.audioCodec.SetChannelLayout(e.audioDecoder.ChannelLayout())
e.audioCodec.SetChannels(e.audioDecoder.Channels())
e.audioCodec.SetBitRate(192000) // 192 kbps for YouTube quality

e.audioStream.SetTimeBase(ffmpeg.AVRational{Num: 1, Den: e.audioCodec.SampleRate()})

ret = ffmpeg.AVCodecOpen2(e.audioCodec, audioEncoder, nil)
if ret < 0 {
	return fmt.Errorf("failed to open audio encoder: %d", ret)
}

ret = ffmpeg.AVCodecParametersFromContext(e.audioStream.Codecpar(), e.audioCodec)
if ret < 0 {
	return fmt.Errorf("failed to copy audio encoder parameters: %d", ret)
}
```

3. **Update `Close()` to cleanup audio resources:**

```go
func (e *Encoder) Close() error {
	// Write trailer before cleanup
	if e.formatCtx != nil {
		ffmpeg.AVWriteTrailer(e.formatCtx)

		if e.formatCtx.Pb() != nil {
			ffmpeg.AVIOClosep(&e.formatCtx.Pb())
		}
	}

	// Free decoder
	if e.audioDecoder != nil {
		ffmpeg.AVCodecFreeContext(&e.audioDecoder)
	}

	// Free encoders
	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}
	if e.audioCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.audioCodec)
	}

	// Close input audio
	if e.audioInputCtx != nil {
		ffmpeg.AVFormatCloseInput(&e.audioInputCtx)
	}

	// Free output format context
	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
```

**Verification:**
```bash
go test -v ./internal/encoder
ffprobe testdata/poc-video.mp4 2>&1 | grep Stream
# Should show both video and audio streams
```

---

### Step 2.2: Implement Audio Transcoding (1-2 hours)

**What:** Read audio packets from WAV, decode, encode to AAC, write to output

**Actions:**

1. **Add `ProcessAudio()` method to `encoder.go`:**

```go
// ProcessAudio reads all audio from input file, transcodes to AAC, writes to output
// This should be called before or during video frame writing
func (e *Encoder) ProcessAudio() error {
	pkt := ffmpeg.AVPacketAlloc()
	defer ffmpeg.AVPacketFree(&pkt)

	frame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&frame)

	// Read all packets from input audio
	for {
		ret := ffmpeg.AVReadFrame(e.audioInputCtx, pkt)
		if ret < 0 {
			if ret == ffmpeg.AVERROR_EOF {
				break // End of file
			}
			return fmt.Errorf("error reading audio packet: %d", ret)
		}

		// Only process audio stream packets
		if pkt.StreamIndex() != 0 { // Assuming audio is stream 0 in WAV
			ffmpeg.AVPacketUnref(pkt)
			continue
		}

		// Decode packet
		ret = ffmpeg.AVCodecSendPacket(e.audioDecoder, pkt)
		ffmpeg.AVPacketUnref(pkt)

		if ret < 0 {
			return fmt.Errorf("error sending audio packet to decoder: %d", ret)
		}

		// Receive decoded frames
		for {
			ret = ffmpeg.AVCodecReceiveFrame(e.audioDecoder, frame)
			if ret == ffmpeg.AVERROR_EAGAIN || ret == ffmpeg.AVERROR_EOF {
				break
			}
			if ret < 0 {
				return fmt.Errorf("error decoding audio frame: %d", ret)
			}

			// Set PTS for encoder
			frame.SetPts(e.nextAudioPts)
			e.nextAudioPts += int64(frame.NbSamples())

			// Encode frame to AAC
			ret = ffmpeg.AVCodecSendFrame(e.audioCodec, frame)
			if ret < 0 {
				return fmt.Errorf("error sending frame to audio encoder: %d", ret)
			}

			// Receive encoded packets
			for {
				encodedPkt := ffmpeg.AVPacketAlloc()
				ret = ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)

				if ret == ffmpeg.AVERROR_EAGAIN || ret == ffmpeg.AVERROR_EOF {
					ffmpeg.AVPacketFree(&encodedPkt)
					break
				}
				if ret < 0 {
					ffmpeg.AVPacketFree(&encodedPkt)
					return fmt.Errorf("error receiving audio packet: %d", ret)
				}

				// Set stream index and rescale timestamps
				encodedPkt.SetStreamIndex(e.audioStream.Index())
				ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

				// Write to output
				ret = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
				ffmpeg.AVPacketFree(&encodedPkt)

				if ret < 0 {
					return fmt.Errorf("error writing audio packet: %d", ret)
				}
			}

			ffmpeg.AVFrameUnref(frame)
		}
	}

	// Flush decoder
	ffmpeg.AVCodecSendPacket(e.audioDecoder, nil)
	// ... receive remaining frames (similar loop as above) ...

	// Flush encoder
	ffmpeg.AVCodecSendFrame(e.audioCodec, nil)
	// ... receive remaining packets (similar loop as above) ...

	return nil
}
```

2. **Add test for audio processing:**

```go
func TestAudioProcessing(t *testing.T) {
	config := encoder.Config{
		OutputPath: "testdata/poc-audio.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
		AudioPath:  "testdata/dream.wav",
	}

	enc, err := encoder.New(config)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	// Process audio in background while we write black frames
	go func() {
		if err := enc.ProcessAudio(); err != nil {
			t.Errorf("ProcessAudio() failed: %v", err)
		}
	}()

	// Write 30 black frames (1 second at 30fps)
	blackFrame := make([]byte, 1280*720*3)
	for i := 0; i < 30; i++ {
		if err := enc.WriteFrame(blackFrame); err != nil {
			t.Fatalf("WriteFrame() failed: %v", err)
		}
	}
}
```

**Verification:**
```bash
go test -v ./internal/encoder -run TestAudioProcessing
ffprobe testdata/poc-audio.mp4 2>&1 | grep -E '(Stream|Duration)'
# Should show both audio and video streams with correct durations
mpv testdata/poc-audio.mp4
# Should hear audio from dream.wav with black video
```

---

### Step 2.3: Phase 2 Validation (1 hour)

**Checklist:**

- [ ] Audio demuxer opens `dream.wav` successfully
- [ ] Audio decoder and AAC encoder configured correctly
- [ ] `ProcessAudio()` transcodes entire WAV file without errors
- [ ] Output video contains both video and audio streams
- [ ] Test with playback:
  ```bash
  go test -v ./internal/encoder -run TestAudioProcessing
  ffprobe testdata/poc-audio.mp4 2>&1 | grep -E '(Stream|Duration)'
  # Should show: Stream #0:0 (video) and Stream #0:1 (audio)
  mpv testdata/poc-audio.mp4
  # Should play black video with dream.wav audio
  ```

**Common Issues:**

| Issue | Cause | Solution |
|-------|-------|----------|
| No audio stream | Decoder not initialized | Check `AVCodecOpen2()` return code |
| Choppy audio | Incorrect sample format | Verify AAC uses `AVSampleFmtFltp` |
| Audio duration mismatch | PTS not set correctly | Ensure `frame.SetPts()` increments by sample count |
| "Invalid argument" errors | Timebase issue | Check `AVPacketRescaleTs()` parameters |

**Success Criteria:**
- ✅ `TestAudioProcessing` passes
- ✅ ffprobe shows both audio and video streams
- ✅ Audio duration matches input WAV file
- ✅ No audio glitches or pops in playback

**Decision Point:**
✅ **Proceed to Phase 3** if audio transcoding works correctly
❌ **Debug audio pipeline** if errors occur (check FFmpeg logs with `av_log_set_level`)

---

## Phase 3: Main Integration with 2-Pass Architecture (Day 2-3, ~6-7 hours)

**Goal:** Integrate ffmpeg-go encoder into Jivefire's main pipeline while **preserving the sliding buffer sync mechanism**

**Critical:** The sliding buffer pattern (pre-fill with FFTSize, advance by samplesPerFrame) MUST be preserved. This is the sync mechanism.

### Step 3.1: Integrate Encoder into Pass 2 (3-4 hours)

**What:** Replace `exec.Command` FFmpeg with ffmpeg-go encoder in `generateVideo()` function

**Key Requirement:** The sliding buffer loop must remain unchanged - we're only replacing the output mechanism, not the frame generation logic.

**Actions:**

1. **Backup current `cmd/jivefire/main.go`:**
```bash
cp cmd/jivefire/main.go cmd/jivefire/main.go.backup
```

2. **Locate the Pass 2 video generation code (around line 140-220):**

Current structure:
```go
func generateVideo(config config.Config, profile *audio.AudioProfile) error {
	// Set up FFmpeg command
	ffmpegCmd := exec.Command("ffmpeg", ...)
	stdin, _ := ffmpegCmd.StdinPipe()
	ffmpegCmd.Start()

	// Sliding buffer setup
	samplesPerFrame := config.SampleRate / config.FPS
	fftBuffer := make([]float32, config.FFTSize)

	// Pre-fill buffer (CRITICAL)
	samples, _ := streamingReader.ReadSamples(config.FFTSize)
	copy(fftBuffer, samples)

	// Frame loop with sliding buffer
	for frameNum := 0; frameNum < totalFrames; frameNum++ {
		// FFT on current buffer
		analyzer.PerformFFT(fftBuffer, ...)

		// Render frame using FFT results
		frameData := renderer.DrawFrame(...)

		// Write to FFmpeg stdin
		stdin.Write(frameData)

		// Advance sliding buffer (CRITICAL SYNC POINT)
		newSamples, _ := streamingReader.ReadSamples(samplesPerFrame)
		copy(fftBuffer, fftBuffer[samplesPerFrame:])  // Shift left
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)  // Append new
	}

	stdin.Close()
	ffmpegCmd.Wait()
}
```

3. **Replace FFmpeg command setup with encoder:**

```go
func generateVideo(config config.Config, profile *audio.AudioProfile) error {
	// Create streaming reader (unchanged)
	streamingReader, err := audio.NewStreamingReader(config.InputAudio)
	if err != nil {
		return fmt.Errorf("failed to create streaming reader: %w", err)
	}
	defer streamingReader.Close()

	// Initialize ffmpeg-go encoder
	encoderConfig := encoder.Config{
		OutputPath: config.Output,
		Width:      config.Width,
		Height:     config.Height,
		Framerate:  config.FPS,
		AudioPath:  config.InputAudio,  // Encoder will demux this separately
	}

	enc, err := encoder.New(encoderConfig)
	if err != nil {
		return fmt.Errorf("failed to create encoder: %w", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		return fmt.Errorf("failed to initialize encoder: %w", err)
	}

	// Start audio processing in background
	// (encoder's ProcessAudio will handle the WAV demuxing)
	go func() {
		if err := enc.ProcessAudio(); err != nil {
			log.Printf("Audio processing error: %v", err)
		}
	}()

	// SLIDING BUFFER SETUP (UNCHANGED - CRITICAL)
	samplesPerFrame := config.SampleRate / config.FPS
	fftBuffer := make([]float32, config.FFTSize)

	// Pre-fill buffer with first FFTSize samples (CRITICAL FOR SYNC)
	samples, err := streamingReader.ReadSamples(config.FFTSize)
	if err != nil {
		return fmt.Errorf("failed to pre-fill buffer: %w", err)
	}
	copy(fftBuffer, samples)

	// Calculate total frames
	totalFrames := config.Duration * config.FPS

	// FRAME LOOP WITH SLIDING BUFFER (PRESERVE EXACTLY)
	for frameNum := 0; frameNum < totalFrames; frameNum++ {
		// Perform FFT on current buffer window
		freqData := audio.PerformFFT(fftBuffer, config.FFTSize)

		// Apply scaling from Pass 1 analysis
		scaledData := audio.ApplyScaling(freqData, profile.OptimalBaseScale)

		// Render visualization frame
		frameData := renderer.DrawFrame(scaledData, config, frameNum)

		// Write frame to encoder (REPLACE stdin.Write)
		if err := enc.WriteFrame(frameData); err != nil {
			return fmt.Errorf("failed to write frame %d: %w", frameNum, err)
		}

		// ADVANCE SLIDING BUFFER (CRITICAL - DO NOT MODIFY)
		newSamples, err := streamingReader.ReadSamples(samplesPerFrame)
		if err != nil {
			// Handle EOF gracefully
			if frameNum < totalFrames-1 {
				return fmt.Errorf("unexpected EOF at frame %d: %w", frameNum, err)
			}
			break
		}

		// Shift buffer left by samplesPerFrame (THIS IS THE SYNC MECHANISM)
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		// Append new samples to end
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)

		// Progress reporting (optional)
		if frameNum%30 == 0 {
			fmt.Printf("\rProgress: %d/%d frames (%.1f%%)",
				frameNum, totalFrames, float64(frameNum)/float64(totalFrames)*100)
		}
	}

	fmt.Println("\nVideo generation complete")
	return nil
}
```

**Key Changes:**
- ❌ Removed `exec.Command` and FFmpeg process management
- ❌ Removed stdin pipe writes
- ✅ Added encoder initialization
- ✅ Added `ProcessAudio()` goroutine for audio handling
- ✅ **PRESERVED** sliding buffer pre-fill
- ✅ **PRESERVED** sliding buffer shift-and-append pattern
- ✅ **PRESERVED** samplesPerFrame advancement

**What NOT to change:**
- ❌ DO NOT modify samplesPerFrame calculation
- ❌ DO NOT change buffer pre-fill logic
- ❌ DO NOT alter the shift-and-append pattern
- ❌ DO NOT add manual PTS management (encoder handles this)

4. **Update imports in `main.go`:**

```go
import (
	"fmt"
	"log"

	"github.com/yourusername/jivefire/internal/audio"
	"github.com/yourusername/jivefire/internal/config"
	"github.com/yourusername/jivefire/internal/encoder"  // ADD THIS
	"github.com/yourusername/jivefire/internal/renderer"
)
```

**Verification:**
```bash
# Build and test
go build ./cmd/jivefire
./jivefire -input testdata/dream.wav -output test-integration.mp4

# Compare with original
ffprobe test-integration.mp4 2>&1 | grep -E '(Stream|Duration)'
# Should show identical duration and stream structure

# Visual/audio verification
mpv test-integration.mp4
# Verify visualization bars move in sync with audio
```

---

### Step 3.2: Preserve Pass 1 (Audio Analysis) - No Changes Needed (30 min validation)

**What:** Verify Pass 1 (audio analysis) remains completely unchanged

**Actions:**

1. **Confirm `internal/audio/analyzer.go` AnalyzeAudio() is untouched:**

```go
// This function should remain EXACTLY as-is
func AnalyzeAudio(config Config) (*AudioProfile, error) {
	reader, err := NewStreamingReader(config.InputAudio)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	// Sliding buffer for analysis (same pattern as Pass 2)
	samplesPerFrame := config.SampleRate / config.FPS
	fftBuffer := make([]float32, config.FFTSize)

	// Pre-fill
	samples, _ := reader.ReadSamples(config.FFTSize)
	copy(fftBuffer, samples)

	globalPeak := float32(0.0)

	// Analyze all frames
	for /* each frame */ {
		freqData := PerformFFT(fftBuffer, config.FFTSize)
		// ... peak detection ...

		// Advance buffer (same as Pass 2)
		newSamples, _ := reader.ReadSamples(samplesPerFrame)
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples)
	}

	// Calculate optimal scale
	profile := &AudioProfile{
		GlobalPeak:        globalPeak,
		OptimalBaseScale:  0.85 / globalPeak,
	}

	return profile, nil
}
```

2. **Run Pass 1 tests to ensure no regressions:**

```bash
go test -v ./internal/audio -run TestAnalyzeAudio
# Should pass without modification
```

**Success Criteria:**
- ✅ No changes to `internal/audio/analyzer.go`
- ✅ AnalyzeAudio() tests still pass
- ✅ AudioProfile calculation unchanged

---

### Step 3.3: Sync Verification Testing (1-2 hours)

**What:** Verify that audio and video remain synchronized with new encoder

**Critical Tests:**

1. **Frame-accurate sync test:**

```go
// Add to internal/encoder/encoder_test.go
func TestAudioVideoSync(t *testing.T) {
	config := encoder.Config{
		OutputPath: "testdata/sync-test.mp4",
		Width:      1280,
		Height:     720,
		Framerate:  30,
		AudioPath:  "testdata/dream.wav",
	}

	enc, err := encoder.New(config)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	defer enc.Close()

	if err := enc.Initialize(); err != nil {
		t.Fatalf("Initialize() failed: %v", err)
	}

	// Start audio processing
	go func() {
		if err := enc.ProcessAudio(); err != nil {
			t.Errorf("ProcessAudio() failed: %v", err)
		}
	}()

	// Write frames with embedded frame numbers
	// (to visually verify sync in playback)
	for i := 0; i < 300; i++ { // 10 seconds at 30fps
		frame := make([]byte, 1280*720*3)
		// TODO: Render frame number into frame data

		if err := enc.WriteFrame(frame); err != nil {
			t.Fatalf("WriteFrame(%d) failed: %v", i, err)
		}
	}
}
```

2. **Manual verification with mpv:**

```bash
# Build and generate test video
go test -v ./internal/encoder -run TestAudioVideoSync
mpv testdata/sync-test.mp4 --no-audio-display

# Verify: Frame numbers should match audio timing
# At 5 seconds: should show frame 150
# At 10 seconds: should show frame 300
```

3. **Compare sync with original implementation:**

```bash
# Generate with original FFmpeg exec
git checkout main
go build -o jivefire-original ./cmd/jivefire
./jivefire-original -input testdata/dream.wav -output original.mp4

# Generate with ffmpeg-go
git checkout ffmpeg-go-integration
go build -o jivefire-new ./cmd/jivefire
./jivefire-new -input testdata/dream.wav -output new.mp4

# Frame-by-frame comparison
ffmpeg -i original.mp4 -vf "select=eq(n\,150)" -frames:v 1 original-frame150.png
ffmpeg -i new.mp4 -vf "select=eq(n\,150)" -frames:v 1 new-frame150.png

# Visual diff
compare original-frame150.png new-frame150.png diff-frame150.png
# Should show minimal/no differences
```

**Success Criteria:**
- ✅ Audio and video play in perfect sync
- ✅ No drift over 30+ seconds of playback
- ✅ Frame content matches original implementation at any timestamp
- ✅ FFmpeg reports identical duration for both streams

---

### Step 3.4: Phase 3 Validation (1 hour)

**Checklist:**

- [ ] Pass 1 (audio analysis) unchanged and tests pass
- [ ] Pass 2 (video generation) uses ffmpeg-go encoder
- [ ] Sliding buffer pattern preserved exactly
- [ ] Audio/video synchronization verified
- [ ] No `exec.Command` calls remain in `main.go`
- [ ] Output quality matches original implementation
- [ ] All tests pass: `go test ./...`

**Integration Test:**
```bash
# Full end-to-end test
go run ./cmd/jivefire \
	-input testdata/dream.wav \
	-output final-integration-test.mp4

# Verify both passes completed
ffprobe final-integration-test.mp4 2>&1 | grep -E '(Stream|Duration)'

# Visual/audio verification
mpv final-integration-test.mp4

# Frame-accurate comparison with original
diff <(ffprobe -show_frames original.mp4 2>&1) \
     <(ffprobe -show-frames final-integration-test.mp4 2>&1)
# Should show identical frame counts and timestamps
```

**Common Issues:**

| Issue | Cause | Solution |
|-------|-------|----------|
| Audio/video drift | Sliding buffer modified | Revert to exact copy/shift pattern |
| Bars not synced to audio | Wrong samplesPerFrame | Verify: sampleRate / FPS |
| Visualization different | OptimalBaseScale not applied | Check Pass 2 uses profile from Pass 1 |
| Crashes mid-generation | FFmpeg buffer overflow | Check frame size matches config |

**Rollback Plan:**
```bash
git diff cmd/jivefire/main.go > phase3-changes.patch
git checkout main  # Revert to working version
# Debug and re-apply with: git apply phase3-changes.patch
```

**Decision Point:**
✅ **Proceed to Phase 4** if sync is perfect and output matches original
❌ **Debug sliding buffer** if any sync issues detected

---

## Phase 4: Finalization & Documentation (Day 3, ~3-4 hours)

**Goal:** Update documentation, clean up code, prepare for production

### Step 4.1: Update Dependencies & Documentation (1-2 hours)

**What:** Finalize go.mod, README, and project docs

**Actions:**

1. **Update `go.mod` with ffmpeg-go:**

```bash
go mod tidy
go mod verify
```

2. **Update README.md:**

```markdown
# Jivefire

...

## Installation

### Prerequisites
- Go 1.21+
- ~~External FFmpeg binary~~ (no longer required!)

### Build
\`\`\bash
go build ./cmd/jivefire
\`\`\`

## How It Works

Jivefire uses a 2-pass architecture:

1. **Pass 1 (Analysis):** Scans entire audio file with sliding FFT buffer
   to calculate optimal visualization scaling
2. **Pass 2 (Rendering):** Generates video frames using same sliding buffer
   pattern, ensuring perfect audio/video synchronization

### Technical Details
- **Video Encoding:** H.264 (libx264) via embedded FFmpeg
- **Audio Encoding:** AAC (192 kbps) via embedded FFmpeg
- **A/V Sync:** Sliding buffer with samplesPerFrame advancement
- **FFT Processing:** 2048-sample window, advancing by samplesPerFrame each frame

## License

GPL v3 (due to ffmpeg-go dependency)
```

3. **Update `LICENSE` file:**

```bash
# Replace current license with GPL v3
curl https://www.gnu.org/licenses/gpl-3.0.txt > LICENSE
```

4. **Document architecture in `docs/ARCHITECTURE.md`:**

```markdown
# Jivefire Architecture

## Overview
Jivefire is a 2-pass audio visualizer built in Go.

## Pass 1: Audio Analysis
**File:** `internal/audio/analyzer.go`

Purpose: Calculate optimal visualization scaling by finding global peak.

Process:
1. Open WAV file with StreamingReader
2. Pre-fill FFT buffer with FFTSize samples
3. For each video frame:
   - Perform FFT on current buffer window
   - Track maximum frequency magnitude
   - Advance buffer by samplesPerFrame (shift + append)
4. Return AudioProfile with OptimalBaseScale = 0.85 / GlobalPeak

## Pass 2: Video Rendering
**File:** `cmd/jivefire/main.go`

Purpose: Generate video frames synchronized to audio.

Process:
1. Initialize ffmpeg-go encoder with output config
2. Start background audio processing (WAV → AAC)
3. Open WAV with StreamingReader (separate instance)
4. Pre-fill FFT buffer with FFTSize samples (same as Pass 1)
5. For each video frame:
   - Perform FFT on current buffer window
   - Apply OptimalBaseScale from Pass 1
   - Render visualization frame
   - Write frame to encoder
   - Advance buffer by samplesPerFrame (CRITICAL SYNC)
6. Close encoder (finalizes MP4)

## Synchronization Mechanism

**Key Insight:** Audio/video sync is maintained by the sliding buffer pattern, NOT by manual timestamp management.

### Sliding Buffer Pattern
\`\`\`go
// Setup
samplesPerFrame := sampleRate / FPS  // e.g., 44100 / 30 = 1470
fftBuffer := make([]float32, FFTSize)  // e.g., 2048

// Pre-fill
samples, _ := reader.ReadSamples(FFTSize)
copy(fftBuffer, samples)

// Frame loop
for each frame {
    // Use current buffer for FFT
    freqData := PerformFFT(fftBuffer)

    // Render and encode frame
    frameData := DrawFrame(freqData)
    encoder.WriteFrame(frameData)

    // Advance buffer (THIS MAINTAINS SYNC)
    newSamples, _ := reader.ReadSamples(samplesPerFrame)
    copy(fftBuffer, fftBuffer[samplesPerFrame:])  // Shift left
    copy(fftBuffer[FFTSize-samplesPerFrame:], newSamples)  // Append new
}
\`\`\`

### Why This Works
- Each video frame advances exactly `samplesPerFrame` samples
- Frame N uses audio samples [N * samplesPerFrame, N * samplesPerFrame + FFTSize]
- Frame timestamps automatically align because:
  - Video PTS = N (where N is frame number)
  - Audio samples at position N * samplesPerFrame correspond to time N / FPS
  - Therefore: video frame N displays FFT of audio at time N / FPS

### Critical Requirements
1. **DO NOT** modify samplesPerFrame calculation
2. **DO NOT** change buffer pre-fill logic
3. **DO NOT** alter shift-and-append pattern
4. **DO NOT** add manual PTS offsets

## FFmpeg Integration

**Library:** ffmpeg-go v0.6.0 (CGO bindings to FFmpeg 6.1)

### Audio Path
1. Encoder opens input WAV file with `AVFormatOpenInput()`
2. Finds audio stream, sets up PCM decoder
3. Decodes PCM samples, re-encodes to AAC
4. Writes AAC packets to output muxer

### Video Path
1. Main loop renders RGB24 frames
2. `WriteFrame()` converts RGB → YUV420p
3. Encodes to H.264 with libx264
4. Writes H.264 packets to output muxer

### Muxing
- MP4 container with interleaved audio/video packets
- Timebase: video = 1/FPS, audio = 1/sampleRate
- Both streams written to same `AVFormatContext`
```

---

### Step 4.2: Remove Dead Code & Cleanup (1 hour)

**What:** Remove FFmpeg exec code, unused imports

**Actions:**

1. **Search for remaining `exec.Command` usage:**

```bash
grep -r "exec.Command" cmd/ internal/
# Should return no results related to FFmpeg
```

2. **Remove unused imports:**

```bash
go run golang.org/x/tools/cmd/goimports -w ./...
```

3. **Run linter:**

```bash
golangci-lint run ./...
# Fix any issues reported
```

4. **Remove backup files:**

```bash
rm cmd/jivefire/main.go.backup
rm -f bench-*.mp4 test-*.mp4 poc-*.mp4
```

---

### Step 4.3: Final Testing & Validation (1-2 hours)

**What:** Comprehensive test suite run

**Actions:**

1. **Run all tests:**

```bash
go test -v ./... -cover
# Target: >80% coverage in encoder package
```

2. **Build and test executable:**

```bash
go build -o jivefire ./cmd/jivefire

# Test with various inputs
./jivefire -input testdata/dream.wav -output test1.mp4
./jivefire -input testdata/different-audio.wav -output test2.mp4

# Verify outputs
for f in test*.mp4; do
    echo "Testing $f"
    ffprobe "$f" 2>&1 | grep -E '(Stream|Duration)'
    mpv --length=5 "$f"  # Watch first 5 seconds
done
```

3. **Performance test:**

```bash
time ./jivefire -input testdata/dream.wav -output perf-test.mp4
# Document baseline performance for future comparison
```

4. **Memory profile (optional but recommended):**

```bash
go test -memprofile=mem.prof ./internal/encoder
go tool pprof -http=:8080 mem.prof
# Check for memory leaks
```

**Success Criteria:**
- ✅ All tests pass
- ✅ No memory leaks detected
- ✅ Executable size: 60-65MB (includes FFmpeg)
- ✅ Performance within 10% of baseline
- ✅ Output quality identical to original

**Rollback Plan:**
```bash
git tag pre-ffmpeg-go  # Tag current state
git checkout pre-ffmpeg-go  # If critical issues found post-merge
```

---

### Step 4.4: Phase 4 Validation & Release Prep (30 min)

**Final Checklist:**

- [ ] go.mod updated with ffmpeg-go
- [ ] README reflects new architecture (no external FFmpeg)
- [ ] LICENSE changed to GPL v3
- [ ] docs/ARCHITECTURE.md created
- [ ] All `exec.Command` calls removed
- [ ] All tests pass: `go test ./...`
- [ ] Linter clean: `golangci-lint run ./...`
- [ ] Performance documented
- [ ] Binary size acceptable (60-65MB)

**Git Commit:**
```bash
git add .
git commit -m "feat: integrate ffmpeg-go encoder

- Replace external FFmpeg with embedded ffmpeg-go v0.6.0
- Preserve 2-pass architecture with sliding buffer sync
- Update license to GPL v3 (ffmpeg-go requirement)
- Remove exec.Command() pipe overhead
- Add comprehensive encoder tests
- Document architecture in docs/ARCHITECTURE.md

Binary size: 62MB (includes static FFmpeg libraries)
Performance: ~5% faster (no pipe overhead)
Breaking change: License changed from MIT to GPL v3"

git tag v2.0.0  # Major version due to license change
```

---

## Phase 5: Future Enhancements (Optional, Post-Release)

**Enabled by ffmpeg-go integration:**

1. **Interactive UI with Bubbletea**
   - Real-time preview during encoding
   - Progress bars with estimated time remaining
   - Live parameter adjustment

2. **Advanced Encoding Options**
   - GPU acceleration (NVENC, VAAPI, VideoToolbox)
   - Alternative codecs (AV1, VP9, HEVC)
   - Custom quality presets
   - Multi-pass encoding

3. **Advanced Features**
   - Live audio input from microphone
   - Real-time visualization (stream to RTMP)
   - Plugin system for custom visualizations

---

## Rollback Procedures

### Emergency Rollback (if critical issue found)

```bash
# 1. Revert to last stable commit
git log --oneline | head -20 # Find pre-ffmpeg-go commit
git checkout <stable-commit-hash>

# 2. Rebuild old version
go build ./cmd/jivefire

# 3. Use external FFmpeg as before
./jivefire -audio podcast.wav -output video.mp4
```

### Partial Rollback (keep some changes)

```bash
# Revert specific commits
git revert <bad-commit-hash>

# Or reset to specific phase
git reset --hard <phase2-commit>
```

---

## Troubleshooting Guide
          go-version: '1.21'

      - name: Build for all platforms
        run: |
          GOOS=linux GOARCH=amd64 go build -o jivefire-linux-amd64 ./cmd/jivefire
          GOOS=linux GOARCH=arm64 go build -o jivefire-linux-arm64 ./cmd/jivefire
          GOOS=darwin GOARCH=amd64 go build -o jivefire-darwin-amd64 ./cmd/jivefire
          GOOS=darwin GOARCH=arm64 go build -o jivefire-darwin-arm64 ./cmd/jivefire

      - name: Create release
        uses: softprops/action-gh-release@v1
        with:
          files: jivefire-*
```

---

### Step 4.4: Testing & Validation (1-2 hours)

**Final Acceptance Testing:**

```bash
# 1. Clean build from scratch
go clean -cache
go build ./cmd/jivefire

# 2. Test with actual podcast audio
./jivefire -audio ~/podcasts/linuxmatters-ep99.wav -output lm99.mp4

# 3. Verify output quality
ffprobe lm99.mp4
mpv lm99.mp4

# 4. Upload to YouTube (unlisted) and verify:
#    - Video plays without buffering
#    - Audio is in sync
#    - Quality is acceptable
#    - No encoding artifacts

# 5. Run full test suite
go test -v ./...

# 6. Build for all platforms
make release # or manual GOOS/GOARCH builds
```

**Checklist:**

- [ ] All tests pass
- [ ] Video output identical to old version (visual inspection)
- [ ] Audio/video sync perfect across full podcast length
- [ ] Binary size acceptable (~70-80MB)
- [ ] Cross-platform builds succeed
- [ ] LICENSE updated to GPL v3
- [ ] README.md updated with new build instructions
- [ ] FFmpeg attribution added to documentation
- [ ] No external FFmpeg dependency remains

---

### Step 4.5: Git History Cleanup (Optional, 30 minutes)

**What:** Squash POC commits for cleaner history

**Actions:**

```bash
# Create feature branch
git checkout -b ffmpeg-go-integration

# Interactive rebase to squash commits
git rebase -i main

# In editor, mark POC/experimental commits as 'squash'
# Keep major phase commits as separate commits

# Final commits should be:
# 1. "feat: add ffmpeg-go dependency and encoder package"
# 2. "feat: implement AAC audio encoding"
# 3. "feat: integrate encoder into main pipeline"
# 4. "docs: update LICENSE to GPL v3 and README for standalone binary"
```

---

## Rollback Procedures

### Emergency Rollback (if critical issue found)

```bash
# 1. Revert to last stable commit
git log --oneline | head -20 # Find pre-ffmpeg-go commit
git checkout <stable-commit-hash>

# 2. Rebuild old version
go build ./cmd/jivefire

# 3. Use external FFmpeg as before
./jivefire -audio podcast.wav -output video.mp4
```

### Partial Rollback (keep some changes)

```bash
# Revert specific commits
git revert <bad-commit-hash>

# Or reset to specific phase
git reset --hard <phase2-commit>
```

---

## Troubleshooting Guide

### Compilation Issues

**Error: `undefined reference to 'av_*'`**

- **Cause:** Linking issue with static libraries
- **Fix:** Verify `CGO_ENABLED=1` and C compiler installed
- **Command:** `go env CGO_ENABLED` (should output `1`)

**Error: `fatal error: libavcodec/avcodec.h: No such file or directory`**

- **Cause:** ffmpeg-go headers not found
- **Fix:** Run `go mod download` and `go mod verify`

### Runtime Issues

**Error: `H.264 encoder not found`**

- **Cause:** Static library missing x264
- **Fix:** Re-download ffmpeg-go: `go get -u github.com/csnewman/ffmpeg-go@latest`

**Error: `failed to open output file`**

- **Cause:** Permission or path issue
- **Fix:** Verify directory exists and is writable: `touch test.mp4 && rm test.mp4`

**Video plays but no audio**

- **Cause:** Audio stream not muxed correctly
- **Fix:** Check `ffprobe` output - should show both streams
- **Debug:** Add logging to `WriteAudio()` to verify samples received

**Audio/video out of sync**

- **Cause:** PTS calculation error
- **Fix:** Verify timebase settings in `Initialize()`
- **Debug:** Print PTS values to ensure linear progression

### Performance Issues

**Encoding slower than expected**

- **Possible Causes:**
  1. Sync bottleneck (audio/video goroutines blocking)
  2. Memory allocation overhead
  3. Incorrect preset (using 'slower' instead of 'medium')

- **Diagnostics:**
```bash
# Profile CPU usage
go run -cpuprofile=cpu.prof ./cmd/jivefire -audio test.wav -output test.mp4
go tool pprof cpu.prof

# Check goroutine activity
go run -trace=trace.out ./cmd/jivefire -audio test.wav -output test.mp4
go tool trace trace.out
```

### Quality Issues

**Video looks blurry**

- **Cause:** CRF value too high
- **Fix:** Lower CRF in `Initialize()` (try CRF 20 instead of 23)

**File size much larger than expected**

- **Cause:** Bitrate too high or CRF too low
- **Fix:** Verify H.264 settings match old FFmpeg command

---

## Success Metrics

### Technical Metrics

- **Build Success:** All platforms (Linux amd64/arm64, macOS amd64/arm64) build without errors
- **Test Pass Rate:** 100% of existing tests still pass
- **Performance:** Encoding speed within 10% of old version
- **Quality:** Output bit-perfect match or visually indistinguishable
- **Binary Size:** 70-80MB (acceptable for standalone distribution)

### User Experience Metrics

- **Installation Simplified:** Users no longer need to install FFmpeg separately
- **Error Rate:** No increase in encoding failures vs. old version
- **YouTube Compatibility:** Videos upload and play without issues

---

## Timeline Summary

| Phase | Duration | Key Deliverables |
|-------|----------|------------------|
| Phase 1: POC | 4-5 hours | Working H.264 video encoder, single frame test |
| Phase 2: Audio | 4-5 hours | WAV demuxing, AAC transcoding, audio stream integration |
| Phase 3: Integration | 6-7 hours | Main pipeline integration with sliding buffer preservation |
| Phase 4: Finalization | 3-4 hours | Documentation, licensing, testing, cleanup |
| **Total** | **17-21 hours (~2.5 days)** | Standalone Jivefire binary with embedded FFmpeg |

**Key Change from Original Estimate:** Reduced from 3 days to 2.5 days because we're **demuxing WAV instead of re-encoding audio from samples**, which simplifies Phase 2 significantly.

---

## Post-Implementation

### Immediate Next Steps

1. **Release Preparation:**
   - Tag release: `git tag v1.0.0-ffmpeg-go`
   - Build all platforms: `make release`
   - Create GitHub release with binaries

2. **User Communication:**
   - Update website/blog with standalone binary announcement
   - Notify existing users via mailing list/Discord
   - Create migration guide for existing scripts

3. **Monitoring:**
   - Watch for user-reported issues in first week
   - Monitor GitHub issues for platform-specific problems
   - Collect feedback on installation simplicity

### Future Enhancements (Phase 5+)

**Low Priority:**
- Hardware acceleration (NVENC, VAAPI, VideoToolbox)
- Alternative codecs (AV1, VP9)
- Custom quality presets
- Real-time preview during encoding

**Interactive UI (Strategic Goal):**
- Bubbletea TUI with live progress
- Parameter adjustment during encoding
- Batch processing queue
- Live preview/scrubbing

---

## References

### Documentation
- [ffmpeg-go README](https://github.com/csnewman/ffmpeg-go)
- [FFmpeg C API Documentation](https://ffmpeg.org/doxygen/trunk/)
- [Go CGO Documentation](https://pkg.go.dev/cmd/cgo)

### Example Code
- [ffmpeg-go transcode example](https://github.com/csnewman/ffmpeg-go/tree/main/examples/transcode)
- [FFmpeg encoding guide](https://trac.ffmpeg.org/wiki/Encode/H.264)

### Jivefire Internal
- [FFMPEG-GO-EVALUATION.md](./FFMPEG-GO-EVALUATION.md) - Suitability analysis
- [REFACTORING.md](./REFACTORING.md) - Current architecture
- [2PASS-PLAN.md](./2PASS-PLAN.md) - Original design

---

## Appendix A: Quick Reference

### Common ffmpeg-go Patterns

**Allocate and free structures:**
```go
frame := ffmpeg.AVFrameAlloc()
defer ffmpeg.AVFrameFree(&frame)

pkt := ffmpeg.AVPacketAlloc()
defer ffmpeg.AVPacketFree(&pkt)
```

**String handling:**
```go
// Go string to C string
cstr := ffmpeg.AllocCStr("path/to/file.mp4")

// Remember: ffmpeg-go handles freeing of AllocCStr automatically
```

**Error handling:**
```go
ret := ffmpeg.AVSomeFunction()
if ret < 0 {
	return fmt.Errorf("operation failed: %d", ret)
}
```

**Timestamp conversion:**
```go
ffmpeg.AVPacketRescaleTs(pkt,
	srcTimebase,  // Source timebase (e.g., codec timebase)
	dstTimebase,  // Destination timebase (e.g., stream timebase)
)
```

### YouTube Upload Settings (for reference)

```
Video:
- Codec: H.264
- Profile: High
- Level: 4.2
- Pixel Format: yuv420p
- Framerate: 30fps (or source)
- Resolution: 1280×720 or higher
- Bitrate: Variable (CRF 23 recommended)

Audio:
- Codec: AAC
- Sample Rate: 44.1kHz or 48kHz
- Bitrate: 192 kbps or higher
- Channels: Mono or Stereo

Container: MP4
```

---

## Appendix B: Comparison Matrix

### Old vs New Architecture

| Aspect | External FFmpeg | ffmpeg-go |
|--------|----------------|-----------|
| **Distribution** | Two binaries (jivefire + ffmpeg) | One binary |
| **Installation** | `apt-get install ffmpeg` required | No dependencies |
| **Performance** | Pipe overhead (~5%) | Direct memory access |
| **Debugging** | FFmpeg stderr parsing | Go error messages |
| **Binary Size** | ~10MB + 100MB FFmpeg | ~70MB combined |
| **Licensing** | Separate licenses | GPL v3 required |
| **Cross-compilation** | Requires FFmpeg on target | Static library included |
| **Future UI** | External process limits control | Full programmatic control |

---

## Appendix C: Glossary

- **CGO:** C Go - Go's mechanism for calling C code
- **CRF:** Constant Rate Factor - quality setting for H.264
- **PTS:** Presentation Timestamp - when frame/sample should display
- **Timebase:** Time unit for PTS (e.g., 1/30 second)
- **GOP:** Group of Pictures - keyframe interval
- **yuv420p:** Pixel format with chroma subsampling
- **Muxer:** Multiplexer - combines audio/video into container
- **Static Library:** Pre-compiled library embedded in binary

---

**Document Version:** 1.0
**Last Updated:** 3 November 2025
**Author:** GitHub Copilot (AI Assistant for Martin Wimpress)
