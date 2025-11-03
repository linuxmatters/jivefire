# Project Reorganization Summary

## What Changed

The project has been reorganized from a single `main.go` file into an idiomatic Go project structure following community best practices.

## New Structure

```
visualizer-go/
├── cmd/visualizer/          # Application entry point
│   └── main.go             # CLI, orchestration, and main loop
├── internal/               # Private application packages
│   ├── audio/             # Audio processing
│   │   ├── reader.go      # WAV file reading
│   │   └── fft.go         # FFT analysis, binning, smoothing
│   ├── renderer/          # Frame rendering
│   │   ├── assets.go      # Font & background loading
│   │   └── frame.go       # Bar drawing & composition
│   └── config/            # Configuration constants
│       └── config.go      # All constants in one place
├── assets/                # Static resources
│   ├── bg.png
│   └── Poppins-Regular.ttf
└── testdata/              # Test files
    ├── dream.wav
    └── test.mp4
```

## Benefits

1. **Clear Separation of Concerns**
   - Audio processing is isolated in `internal/audio/`
   - Rendering logic is isolated in `internal/renderer/`
   - Configuration is centralized in `internal/config/`
   - CLI orchestration is in `cmd/visualizer/`

2. **Testability**
   - Each package can be tested independently
   - Easy to add unit tests for audio/renderer packages
   - Mock interfaces can be created for testing

3. **Maintainability**
   - Smaller, focused files (~150-300 lines each)
   - Easy to find and modify specific functionality
   - Clear API boundaries between packages

4. **Standard Go Conventions**
   - `cmd/` for command-line applications
   - `internal/` prevents external imports (keeps implementation private)
   - `assets/` and `testdata/` follow Go community patterns

## Package Responsibilities

### `internal/config`
- All constants: video settings, audio settings, visualization parameters, colors
- Single source of truth for configuration values

### `internal/audio`
- `ReadWAV()` - Load and convert WAV files to float64 samples
- `NewProcessor()` - Create FFT processor
- `ProcessChunk()` - Apply Hanning window and compute FFT
- `BinFFT()` - Frequency binning with CAVA-style processing
- `RearrangeFrequenciesCenterOut()` - Symmetric frequency distribution

### `internal/renderer`
- `LoadBackgroundImage()` - Load and scale PNG backgrounds
- `LoadFont()` - Load TrueType fonts
- `NewFrame()` - Create frame renderer with assets
- `Frame.Draw()` - Render bars with gradient effects
- `DrawCenterText()` / `DrawEpisodeNumber()` - Text overlays
- `WriteRawRGB()` - Output raw RGB24 to FFmpeg

### `cmd/visualizer`
- CLI flag parsing
- Main video generation loop with CAVA algorithm
- Snapshot generation mode
- FFmpeg orchestration
- Performance profiling

## Build & Run

```bash
# Using just (recommended)
just build                                    # Build binary
just snapshot                                 # Quick 10s snapshot → testdata/snapshot.png
just video                                    # Full render → testdata/test.mp4
just run testdata/dream.wav output.mp4       # Custom input/output

# Manual build
go build -o visualizer ./cmd/visualizer

# Run
./visualizer testdata/dream.wav output.mp4

# Snapshot mode (new consolidated syntax)
./visualizer --snapshot=5.0 testdata/dream.wav frame.png
./visualizer -s 5.0 testdata/dream.wav frame.png  # Short form
```

## CLI Argument Handling

The project uses **Kong** for declarative CLI argument parsing:

```go
var CLI struct {
    Input    string   `arg:"" name:"input" help:"Input WAV file" type:"existingfile"`
    Output   string   `arg:"" name:"output" help:"Output file (.mp4 for video, .png for snapshot)"`
    Snapshot *float64 `help:"Generate snapshot at specified time (seconds) instead of full video" short:"s"`
}
```

**Why Kong?**
- **Declarative:** Arguments defined as struct tags, not imperative code
- **Validation:** Built-in file existence checking, type validation
- **Future-proof:** Easy to add more flags/subcommands as features grow
- **Bubbletea compatible:** Clean separation for future TUI integration
- **Professional help:** Automatic help generation with proper formatting

**Migration from stdlib `flag`:**
- **Before:** `--snapshot` (bool) + `--at=<time>` (float64) as separate flags
- **After:** `--snapshot=<time>` as single consolidated flag
- **Benefits:** Cleaner UX, single flag for one feature, short form `-s` available

## Next Steps for Development

1. **Add Tests**
   - Create `internal/audio/fft_test.go`
   - Create `internal/renderer/frame_test.go`
   - Test core algorithms in isolation

2. **Extract More**
   - Consider extracting CAVA algorithm into `internal/audio/cava.go`
   - Consider extracting FFmpeg interface into `internal/video/encoder.go`

3. **Add Godoc Comments**
   - Document exported types and functions
   - Add package-level documentation

4. **Configuration File Support**
   - Could add `internal/config/file.go` for loading config from YAML/JSON
   - Keep constants as defaults, allow override from file

## Verification

✅ Build succeeds: `go build -o visualizer ./cmd/visualizer`
✅ Snapshot mode works: `./visualizer --snapshot --at=5.0 testdata/dream.wav test.png`
✅ All functionality preserved from original implementation
✅ No changes to algorithm or performance characteristics
