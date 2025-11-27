# Jivefire Architecture

**TL;DR:** 2-pass streaming audio visualiser that generates broadcast-ready MP4s from podcast audio. FFmpeg-based audio decoding + ffmpeg-statigo static linking = single deployable binary with broad format support.

---

## Core Design Principles

### 1. **Single Binary Distribution**
Uses [ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo) with embedded static FFmpeg 8.0 libraries (~100MB per platform). No external FFmpeg installation required. Ships for Linux/macOS on amd64/arm64. Hardware acceleration support for NVENC, QuickSync, VideoToolbox, and Vulkan Video.

**Why ffmpeg-statigo specifically?** Pre-built static libraries with GPL-licensed FFmpeg that includes H.264 (libx264), H.265 (x265), AV1 (rav1e/dav1d), and AAC encoders. Produces YouTube-compatible MP4s out of the box.

### 2. **2-Pass Streaming Architecture**
Memory-efficient approach: analyse first, render second.

**Pass 1 (Analysis):**
- Stream audio chunks (2048 samples/frame)
- FFT analysis to determine peak magnitudes across all frames
- Calculates optimal scaling parameters
- Memory footprint: ~50MB for 30-minute audio

**Pass 2 (Rendering):**
- Stream audio again with optimal scaling
- Generate RGB frames on-the-fly
- Encode video + audio simultaneously
- No frame buffering—everything streaming

**Why not single-pass?** Naive approach requires pre-loading entire audio file into memory (600MB for 30 minutes). 2-pass reduces memory by 92% while enabling optimal bar height scaling.

### 3. **Unified FFmpeg Audio Pipeline**
Audio decoding uses ffmpeg-statigo's libavformat/libavcodec, supporting any format FFmpeg handles: MP3, FLAC, WAV, OGG, AAC, and more.

**Why FFmpeg for decoding?** Single decode path for all formats. Audio samples are decoded once and shared between FFT analysis (Pass 1) and AAC encoding (Pass 2). The unified pipeline eliminates the "catch-up" delay that occurred when audio was re-decoded during encoding.

**Architecture:**
- `FFmpegDecoder` implements the `AudioDecoder` interface
- Streaming decode: reads chunks on demand, no full-file buffering
- Automatic stereo-to-mono downmixing for visualisation
- Sample rate preserved for AAC encoding

---

## Processing Pipeline

```
Input Audio (MP3/FLAC/WAV/OGG/AAC/...)
    ↓
FFmpeg Decoder (ffmpeg-statigo, streaming)
    ├─ libavformat for demuxing
    ├─ libavcodec for decoding
    └─ Automatic stereo→mono downmix
    ↓
FFT Analysis (gonum/fourier)
    ├─ 2048-point Hanning window
    ├─ Log-scale frequency binning → 64 bars
    └─ Smooth decay animation (CAVA-style)
    ↓
Frame Renderer (image/draw + custom optimizations)
    ├─ 64 bars with symmetric vertical mirroring
    ├─ Pre-computed alpha tables for gradients
    └─ RGB24 pixel buffer (1280×720)
    ↓
RGB → YUV420P Conversion (Pure Go, parallelised)
    ├─ 8.4× faster than FFmpeg swscale
    ├─ Parallel row processing across CPU cores
    └─ Standard ITU-R BT.601 coefficients
    ↓
ffmpeg-statigo H.264 Encoder (libx264)
    ├─ 30fps video stream
    └─ yuv420p pixel format
    ↓
ffmpeg-statigo AAC Encoder
    ├─ Receives pre-decoded samples via WriteAudioSamples()
    ├─ Audio FIFO buffer (handles frame size mismatches)
    ├─ float32 → float32 planar conversion
    └─ Mono or stereo output
    ↓
MP4 Muxer (libavformat)
    └─ Interleaved audio/video packets
```

---

## Key Technical Choices

### Audio Frame Size Mismatch
FFT analysis requires 2048 samples for frequency resolution, but AAC encoder expects 1024 samples per frame. **Solution:** `SharedAudioBuffer` in `audio/shared_buffer.go` provides thread-safe multi-consumer access with independent read positions—FFT and encoder each consume at their own rate without blocking each other.

### RGB → YUV Conversion
Custom parallelised RGB→YUV420P converter in `encoder/frame.go`:
- Processes image rows in parallel across CPU cores
- Uses Go's standard ITU-R BT.601 coefficients
- **8.4× faster than FFmpeg's swscale** (benchmarked: 346µs vs 2,915µs per 1280×720 frame)
- Strong candidate for extraction as standalone Go module

**Why not FFmpeg's swscale?** While ffmpeg-statigo exposes the full swscale API, benchmarking revealed our parallelised Go implementation significantly outperforms it. FFmpeg's swscale is single-threaded; our implementation distributes row processing across all CPU cores. On a 12-core/24-thread Ryzen 9, parallelisation wins decisively over SIMD.

**Why not Go's `color.RGBToYCbCr()`?** It's fast for single pixels but not parallelised. Our implementation processes multiple rows simultaneously across goroutines, achieving ~10× realtime encoding speed.

### Symmetric Bar Rendering
Bars 0-31 are mirrored to create bars 32-63. Renderer draws upper-left quadrant (1/4 of pixels), then mirrors 3 times:
1. Vertical flip → downward bars (left half)
2. Horizontal flip → upward bars (right half)
3. Both flips → downward bars (right half)

**Result:** 4x rendering speedup via reduced pixel writes.

### Bubbletea Live Preview
Terminal UI shows:
- Pass 1: Real-time FFT bar preview during analysis
- Pass 2: Encoding stats + mini ASCII spectrum visualisation

Preview renders via Unicode blocks (`▁▂▃▄▅▆▇█`) using actual bar heights from renderer. Non-blocking goroutine channels prevent UI updates from stalling the encoding pipeline.

---

## File Structure

```
cmd/jivefire/main.go     → CLI entry, 2-pass coordinator
internal/audio/          → FFmpegDecoder (AudioDecoder interface), FFT analysis
internal/encoder/        → ffmpeg-statigo wrapper, RGB→YUV conversion, FIFO buffer
internal/renderer/       → Frame generation, bar drawing, thumbnail
internal/ui/             → Bubbletea TUI (pass1.go, pass2.go)
internal/config/         → Constants (dimensions, FFT params, colours)
```

---

## Future-Proofing

### go-yuv: Parallelised Colourspace Conversion
The RGB→YUV converter in `encoder/frame.go` is a strong candidate for extraction as a standalone Go module. Benchmarking shows it's **8.4× faster than FFmpeg's swscale** for RGB24→YUV420P conversion at 720p, thanks to goroutine-based parallelisation across CPU cores.

There's currently no pure Go library offering parallelised colourspace conversion. Existing options are either single-threaded (stdlib `color.RGBToYCbCr`) or require CGO FFmpeg bindings. A standalone `go-yuv` module would benefit:
- Video encoding pipelines avoiding FFmpeg dependencies
- Image processing tools needing high-throughput colourspace conversion
- WebRTC/streaming applications with real-time constraints

The FIFO buffer implementation is generic enough for any audio frame size mismatch scenario in Go audio processing pipelines.

FFT bar binning logic mirrors CAVA's approach, making it familiar territory for anyone who's worked with terminal audio visualisers.
