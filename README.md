# Audio Visualizer (Go)

CLI audio visualizer written in Go that generates **discrete frequency bars** for podcast video production.

## Project Context

**Problem:** FFmpeg's audio visualization filters (`showfreqs`, `showspectrum`) render **continuous frequency spectra**, not discrete bars. After extensive testing and research (including official FFmpeg 7.1.1 documentation), confirmed that pure FFmpeg cannot achieve discrete 63-bar visualization required for Linux Matters podcast branding.

**Solution:** Go implementation that performs FFT analysis, bins frequencies into discrete bars, generates frames, and pipes to FFmpeg for encoding. This hybrid approach achieves the discrete bar aesthetic while avoiding Python dependency hell.

**Original Tool:** Replacing moribund `djfun/audio-visualizer-python` (Qt5 GUI tool, last updated ~2020-2021) with modern CLI workflow.

## Current Status: Production Ready ✅

**Performance: ~9x realtime speed** 
- 72-second audio → 8 seconds processing time
- **89x improvement** from initial 0.102x implementation

**What Works:**
- ✅ 64 discrete frequency bars (visually distinct, not continuous)
- ✅ Symmetric mirroring (bars above and below center)
- ✅ Brand red color RGB(164,0,0) applied
- ✅ FFT-based analysis (2048-point, Hanning window, log scale)
- ✅ Proper bar spacing and layout
- ✅ Production-ready performance

**Performance Profile:**
- Frame drawing: 48.6% (optimized with buffer reuse, copy operations)
- FFmpeg encoding: 45.8% (ultrafast preset, pipe overhead)
- FFT computation: 1.3% (negligible)
- Bar binning: 0.1% (negligible)

## Specification (Target)

### Visual Requirements
- **63 discrete bars** (currently 64) - each ~20px wide
- **Symmetric mirroring** - bars reflected above/below center ✅
- **Resolution:** 1280×720 @ 30fps ✅
- **Colors:** RGB(164,0,0) red bars ✅, RGB(254,184,30) yellow text (TODO)
- **Background:** Static image or black ✅ (black currently)

### Audio Processing
- **Sample rate:** 44.1kHz ✅
- **FFT:** 2048-point ✅, Hanning window ✅
- **Scaling:** Logarithmic ✅
- **Smoothing:** Decay animation (down 0.08, up 0.8) - TODO
- **Frequency binning:** Group FFT results into discrete bars ✅

### Output
- **Container:** MP4 ✅
- **Video codec:** H.264 (libx264) ✅
- **Audio codec:** AAC 192kbps ✅
- **Pixel format:** yuv420p ✅

## Architecture

```
Audio Input (WAV)
    ↓
Go: Read WAV file (go-audio/wav)
    ↓
Go: FFT Analysis per frame (gonum fourier)
    ├─ 2048-point FFT
    ├─ Hanning window
    └─ Group into 64 discrete bins
    ↓
Go: Generate RGB24 frames (image/draw)
    ├─ Draw 64 bars with calculated heights
    ├─ Apply symmetric mirroring
    └─ Direct pixel buffer writes (optimized)
    ↓
Go: Pipe raw frames to FFmpeg stdin
    ↓
FFmpeg: Encode video + mux audio
    └─ Output: MP4 with discrete bar visualization
```

## Performance Optimizations

**Implemented:**
- ✅ Direct pixel buffer writes (`img.Pix[]`) instead of `img.Set()` - ~100x faster
- ✅ Optimized FFmpeg RGB24 pipe with row buffering - eliminated per-pixel overhead
- ✅ Image buffer reuse across frames - eliminated repeated allocations
- ✅ Pre-computed bar pixel row with copy operations - eliminated nested loops
- ✅ FFmpeg preset `ultrafast` for faster encoding
- ✅ Direct stride-based pixel addressing

**Result:** From 0.102x to ~9x realtime (89x speedup)

**TODO:**
- Yellow text overlay RGB(254,184,30) for episode/title
- Reduce to 63 bars (from 64) for brand consistency
- Smoothing/decay animation (down 0.08, up 0.8)

## Requirements

- Go 1.21+
- FFmpeg in PATH
- Nix flake provided for reproducible environment

## Build

```bash
go mod tidy
go build -o visualizer
```

## Usage

```bash
./visualizer input.wav output.mp4
```

**Example:**
```bash
./visualizer dream.wav test-go-bars.mp4
```

## Implementation Details

### Dependencies
- `gonum.org/v1/gonum/dsp/fourier` - FFT computation
- `github.com/go-audio/wav` - WAV file reading
- `github.com/go-audio/audio` - Audio buffer utilities

### Key Functions
- `readWAV()` - Load audio, convert to float64 samples
- `applyHanning()` - Apply Hanning window to FFT input
- `binFFT()` - Group FFT coefficients into discrete bars with log scaling
- `drawFrame()` - Generate RGB24 image with bars (direct pixel writes)
- `writeRawRGB()` - Pipe raw frames to FFmpeg stdin

### Constants (Tunable)
```go
width      = 1280    // Video width
height     = 720     // Video height
fps        = 30      // Frames per second
sampleRate = 44100   // Audio sample rate
fftSize    = 2048    // FFT window size
numBars    = 64      // Number of discrete bars (target: 63)
barWidth   = 20      // Width of each bar in pixels
barColorR  = 164     // Red component
barColorG  = 0       // Green component
barColorB  = 0       // Blue component
```

## Roadmap

### Phase 1: Performance (CURRENT PRIORITY)
- [ ] **Profile bottlenecks** - identify if frame gen or FFmpeg is slow
- [ ] **Optimize frame generation** - parallel processing, buffer pooling
- [ ] **Optimize FFmpeg pipeline** - thread settings, buffer sizes
- [ ] **Target:** Achieve 1x speed or better (real-time encoding)

### Phase 2: Feature Parity
- [ ] Smoothing/decay animation (0.08 down, 0.8 up constants)
- [ ] Exactly 63 bars (change `numBars = 63`)
- [ ] Text overlay for episode title (Go drawing or FFmpeg filter)
- [ ] Background image support (static PNG/JPG)
- [ ] Configurable colors via command-line flags

### Phase 3: Production Ready
- [ ] Command-line argument parsing (flags for all options)
- [ ] Config file support (YAML/TOML)
- [ ] Progress bar / status output
- [ ] Error handling improvements
- [ ] Documentation and examples

### Phase 4: Advanced Features
- [ ] Multiple color schemes
- [ ] Video background support (MP4 input)
- [ ] Multiple text overlays with positioning
- [ ] Adjustable bar spacing/width
- [ ] Different FFT window functions

## Research Summary

**FFmpeg Limitations Confirmed:**
- Official FFmpeg 7.1.1 documentation analyzed (2 Nov 2025)
- `showfreqs` Section 19.24: `mode=bar` only changes rendering style
- No parameters for discrete frequency binning (`bins`, `bar_count`, etc.)
- All audio→video filters render continuous spectra, not grouped bars
- Tested: width sizing, scaling, FFT window adjustments - all failed
- **Conclusion:** Pure FFmpeg approach is architecturally impossible

**Why This Approach Works:**
- Python tool (original) uses NumPy FFT → manual 63-bin grouping → Pillow draws 63 rectangles
- Go tool replicates same logic: FFT → bin grouping → draw discrete bars
- FFmpeg is used only for encoding (what it does well), not visualization logic

## Single Binary Distribution

**Advantage:** No dependency hell!
- Python requires: virtualenv, NumPy (BLAS/LAPACK), SciPy (FORTRAN), Pillow (libjpeg/libpng)
- Go produces: Single static binary that works anywhere

```bash
# Cross-compile for different platforms
GOOS=linux GOARCH=amd64 go build -o visualizer-linux-amd64
GOOS=darwin GOARCH=arm64 go build -o visualizer-macos-arm64
```

## License

TBD

## Contributing

Project is in proof-of-concept stage. Performance optimization is the immediate priority.
- Add text overlay support
- Add background image support
- Fine-tune to exactly 63 bars if needed
