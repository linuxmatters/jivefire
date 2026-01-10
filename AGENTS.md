# AGENTS.md

## Setup Commands

- Initialise ffmpeg-statigo submodule and download static FFmpeg libraries: `just setup`

## Build and Test Commands

- Build binary: `just build` (includes version from git tags)
- Run all tests: `just test`
- Test encoding with multiple formats: `just test-encoder` (mp3/flac/wav, mono/stereo)
- Benchmark RGB→YUV conversion: `just bench-yuv`
- Record demo tape: `just vhs`

## Architecture (2-Pass Streaming)

- **Pass 1 (Analysis):** Stream audio through FFT to find peak magnitudes, calculate optimal bar scaling
- **Pass 2 (Rendering):** Re-stream audio, generate RGB frames, encode video+audio simultaneously
- Memory-efficient: ~50MB footprint for 30-minute audio vs 600MB for single-pass

### Key Modules
- `cmd/jivefire/main.go` — CLI entry, 2-pass coordinator
- `internal/audio/` — FFmpegDecoder implements AudioDecoder interface, FFT analysis
- `internal/encoder/` — ffmpeg-statigo wrapper, RGB→YUV conversion, FIFO buffer
- `internal/renderer/` — Frame generation, bar drawing, thumbnail
- `internal/ui/` — Bubbletea TUI (unified progress.go for both passes)
- `internal/config/` — Constants (dimensions, FFT params, colours)

## FFmpeg Integration

- All FFmpeg access through `third_party/ffmpeg-statigo` submodule (FFmpeg 8.0 static bindings)
- `*.gen.go` files in submodule are auto-generated — do not edit
- Audio decoding: `internal/audio/ffmpeg_decoder.go` implements `AudioDecoder` interface
- Video/audio encoding: `internal/encoder/encoder.go` wraps libx264/AAC

## Audio Processing

- FFT size: 2048 samples (Hanning window)
- 64 frequency bars with log-scale binning
- CAVA-style smooth decay: `NoiseReduction=0.77`, `FallAccel=0.028`
- Audio frame size mismatch handled by `AudioFIFO` (FFT needs 2048, AAC expects 1024)

## Performance Patterns

- RGB→YUV conversion in `encoder/frame.go` parallelised across CPU cores (8.4× faster than swscale)
- Frame rendering uses symmetric mirroring (draw 1/4 pixels, mirror 3×)
- Pre-computed intensity/colour tables in `renderer/frame.go`
- Bubbletea UI uses non-blocking goroutine channels

## Code Style

- British English spelling in comments and user-facing text
- All video/audio constants centralised in `internal/config/config.go`
- Embedded assets (fonts, images) in `internal/renderer/assets/`
- CLI uses Kong for argument parsing with custom styled help

## Testing Instructions

- Test audio files in `testdata/` (LMP0.mp3, LMP0.wav, LMP0.flac variants)
- Throwaway test code goes in `testdata/`
- Benchmark tests: `*_bench_test.go` files

## Common Tasks

### Adding a new audio format
1. FFmpeg already handles it—no decoder changes needed (unified pipeline)
2. Add test case to `justfile` following existing patterns

### Modifying visualisation
- Bar colours/dimensions: `internal/config/config.go`
- Bar rendering logic: `internal/renderer/frame.go` (see `Render()` method)
- Gradient/alpha tables: pre-computed in `NewFrame()`

### Changing UI output
- Unified progress UI: `internal/ui/progress.go` (handles both passes)
- Message types: `AnalysisProgress`, `AnalysisComplete`, `RenderProgress`, `RenderComplete`
- Audio profile display persists from Pass 1 through Pass 2
- Video preview: `internal/ui/preview.go`

## Environment

- NixOS development shell via `flake.nix`
- Fish shell for terminal commands
- CGO required (`CGO_ENABLED=1` in build)
- Go 1.24.0 minimum
