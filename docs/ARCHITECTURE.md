# Jivefire Architecture

**TL;DR:** 2-pass streaming audio visualiser that generates broadcast-ready MP4s from podcast audio. Pure Go audio decoding + ffmpeg-statigo static linking = single deployable binary.

---

## The Problem

FFmpeg's audio visualisation filters (`showfreqs`, `showspectrum`) render continuous frequency spectra, not discrete bars. No amount of FFmpeg filter chain kung-fu can achieve the discrete 64-bar aesthetic required for Linux Matters branding. Solution: Do the FFT analysis and bar rendering in Go, pipe frames to FFmpeg for encoding.

**Why Go over Python?** The original `djfun/audio-visualizer-python` tool is a moribund Qt5 GUI with significant debt. Modern podcast production needs a multi-archtitecture tools that's that can integrate into automation pipelines.

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

### 3. **Pure Go Audio Decoders**
Supports WAV, MP3, FLAC via pure Go libraries:
- WAV: `go-audio/wav`
- MP3: `sukus21/go-mp3`
- FLAC: `mewkiz/flac`

**Why pure Go?** Maintains single-binary distribution without codec dependencies. Automatic stereo-to-mono downmixing. Format detection via file extension.

---

## Processing Pipeline

```
Input Audio (WAV/MP3/FLAC)
    ↓
Pure Go Decoder (streaming, 2048 samples/frame)
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
    ├─ Audio FIFO buffer (2048→1024 frame size mismatch)
    ├─ int16/float32 → float32 planar conversion
    └─ Mono→stereo duplication
    ↓
MP4 Muxer (libavformat)
    └─ Interleaved audio/video packets
```

---

## Key Technical Choices

### Audio Frame Size Mismatch
FFT analysis requires 2048 samples for frequency resolution, but AAC encoder expects 1024 samples per frame. **Solution:** Pure Go FIFO buffer in `encoder/encoder.go` handles buffering and frame size conversion without external dependencies.

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
cmd/jivefire/          # CLI entry point, 2-pass coordinator
internal/
  audio/               # Pure Go decoders (WAV/MP3/FLAC)
  ├─ analyzer.go       # FFT analysis, bar binning
  ├─ decoder.go        # AudioDecoder interface
  └─ reader.go         # Streaming reader with format detection

  encoder/             # ffmpeg-statigo wrapper
  ├─ encoder.go        # H.264 + AAC encoding, FIFO buffer
  └─ frame.go          # RGB→YUV conversion (parallelised)

  renderer/            # Frame generation
  └─ frame.go          # Bar drawing, alpha tables, symmetry

  ui/                  # Bubbletea models
  ├─ pass1.go          # Analysis progress + live preview
  └─ pass2.go          # Encoding progress + mini spectrum

  config/              # Constants (dimensions, colours, FFT params)
```

---

## Why This Architecture Works

**Single Responsibility:** Each component does one thing well. Audio decoding is separate from FFT analysis. Frame rendering doesn't know about encoding. Encoder handles video/audio coordination.

**Streaming Everything:** No large memory allocations. Audio chunks flow through FFT → rendering → encoding without buffering.

**Static Linking:** ffmpeg-statigo's pre-built libraries eliminate "works on my machine" FFmpeg version chaos. One binary, guaranteed codec support.

**Performance Where It Matters:** The RGB→YUV conversion and frame rendering optimisations target the actual bottlenecks (measured via profiling), not premature guesses.

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

## Orientation

This a Jivefire, a Go project, that encodes podcast audio file to MP4 videos suitable for uploading to YouTube.

Orientate yourself with the project by reading the documentation (README.md and docs/ARCHITECTURE.md) and analysing the code. This project uses `ffmpeg-statigo` for FFmpeg 8.0 static bindings, included as a git submodule in `vendor/ffmpeg-statigo`.

Sample audio file is in `testdata/`. You should only build and test via `just` commands. We are using NixOS as the host operating system and `flake.nix` provides tooling for the development shell. I use the `fish` shell. If you need to create "throw-away" test code, the put it in `testdata/`.

Never claim your work is "Perfect", "Excellent" or "Production ready". I will judge the quality of the work we do. Never claim something is fixed, working or implemented until we have both confirmed so.

Let me know when you are ready to start collaborating.
