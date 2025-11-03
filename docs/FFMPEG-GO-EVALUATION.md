# ffmpeg-go Evaluation for Jivefire

**Analysis Date:** 3 November 2025
**Evaluated Project:** [csnewman/ffmpeg-go](https://github.com/csnewman/ffmpeg-go)
**Purpose:** Assess suitability for creating a standalone Jivefire binary

---

## Executive Summary

**Recommendation: ✅ SUITABLE** *(Updated: GPL v3 licensing confirmed acceptable)*

ffmpeg-go is **fit for purpose** for Jivefire's requirements to produce YouTube-ready MP4 videos with H.264 video and AAC audio encoding. The project provides pre-built static libraries for all target platforms (Linux and macOS, both amd64 and arm64) with GPL-licensed FFmpeg 6.1 that includes the necessary x264 and AAC encoders.

**Key Finding:** Both **H.264 (via libx264)** and **AAC (native FFmpeg encoder)** are included and verified in the static libraries, meeting YouTube's recommended upload specifications. **Licensing is not a blocker** - Linux Matters team has confirmed GPL v3 is acceptable.

---

## Project Overview

| Attribute | Details |
|-----------|---------|
| **Repository** | github.com/csnewman/ffmpeg-go |
| **License** | MIT (wrapper), GPL (FFmpeg build) |
| **FFmpeg Version** | 6.1 (released 2023) |
| **Latest Release** | v0.6.0 (25 Feb 2024) |
| **Last Activity** | 28 Aug 2025 |
| **Stars** | 68 |
| **Issues** | 0 open |
| **Maturity** | Production-ready |

---

## ✅ Pros

### 1. **Complete YouTube Codec Support**
- ✅ **H.264 video encoding** via `libx264` (YouTube's recommended codec)
- ✅ **AAC audio encoding** via native FFmpeg AAC encoder (YouTube's recommended audio codec)
- ✅ MP4 container support via `libavformat`
- ✅ `yuv420p` pixel format support (YouTube requirement)
- ✅ Verified encoder symbols present in static library: `ff_aac_encoder`, `x264_encoder_encode`

### 2. **True Standalone Binary**
- Static libraries eliminate runtime FFmpeg dependency
- Pre-built binaries: 50-65MB per platform
- No separate FFmpeg installation required
- Simplified distribution: single binary for end users

### 3. **Platform Coverage**
- ✅ Linux amd64 (primary)
- ✅ Linux arm64 (Raspberry Pi, cloud ARM instances)
- ✅ macOS amd64 (Intel Macs)
- ✅ macOS arm64 (Apple Silicon)

All platforms supported by Jivefire's target audience.

### 4. **Rich Codec Ecosystem**
Beyond H.264/AAC, includes:
- **Video:** AV1 (libaom), VP9 (libvpx), Theora
- **Audio:** Opus, Speex, Vorbis, MP3 (libmp3lame)
- **Subtitles:** libass with freetype/harfbuzz rendering
- Future-proofs Jivefire for alternative formats

### 5. **Production-Ready Architecture**
- Auto-generated bindings from FFmpeg headers
- Thin C wrapper preserving FFmpeg API design
- Comprehensive transcode example demonstrates:
  - Decoding (audio/video)
  - Encoding (audio/video)
  - Filtering (buffersrc/buffersink)
  - Muxing (AVFormatContext)
- Error handling via Go error interface
- Memory management wrappers (AllocCStr, Free)

### 6. **GPL-Licensed Build**
- Built with `--enable-gpl` and `--enable-version3`
- Enables x264 (GPL) and other restrictive licenses
- Acceptable for Jivefire as open-source podcast tool
- No commercial licensing concerns for Linux Matters

### 7. **Active Maintenance**
- Recent activity (Aug 2025)
- Automated CI for library rebuilds
- Dependency updates (PR #12: xz bump)
- Zero open issues indicates stability

---

## ⚠️ Cons & Caveats

### 1. **CGO Dependency**
- **Not Pure Go:** Requires C compiler for builds
- Cross-compilation complexity increases
- Build times longer than pure Go
- **Mitigation:** Pre-compile binaries via CI for releases

### 2. **System Library Requirements**
**Linux:**
- `libm` (math)
- `libdl` (dynamic loading)

**macOS:**
- `ApplicationServices`, `CoreVideo`, `CoreMedia`
- `VideoToolbox`, `AudioToolbox`

**Impact:** Standard CGO requirements—typical for Go applications with C dependencies
**Reality:** These system libraries are present on all modern Linux/macOS systems by default. No user installation required.

### 3. **Binary Size Increase**
- Current Jivefire: ~10MB (estimated)
- With ffmpeg-go: ~60-70MB per platform
- **Trade-off:** Standalone convenience vs binary size
- **Assessment:** ✅ **Acceptable** - confirmed not a concern for desktop tool use case

### 4. **No Hardware Acceleration**
- Build configured with `--disable-autodetect`
- No explicit VAAPI, NVENC, VideoToolbox acceleration
- Software encoding only (current state)
- **Impact:** Limited for Jivefire (already fast at ~9x realtime)
- **Future:** Could request hardware-accelerated builds

### 5. **FFmpeg 6.1 (Not Latest)**
- Current FFmpeg stable: 7.1 (as of Nov 2025)
- 6.1 released: 2023
- Missing 1-2 years of FFmpeg improvements
- **Impact:** Minimal for Jivefire's use case (H.264/AAC stable)
- **Mitigation:** x264 uses "head" (latest) build

### 6. **Thin Documentation**
- Primarily relies on C FFmpeg documentation
- Limited Go-specific examples (3 total)
- No high-level Go abstractions
- **Mitigation:** Jivefire team has FFmpeg knowledge, transcode example provides template

### 7. **API Verbosity**
Direct C API means verbose code:
```go
// Current Jivefire (simplified):
cmd := exec.Command("ffmpeg", "-y", "-f", "rawvideo", ...)
stdin, _ := cmd.StdinPipe()
stdin.Write(frameData)

// ffmpeg-go equivalent (much longer):
ctx := ffmpeg.AVFormatAllocContext()
stream := ffmpeg.AVFormatNewStream(ctx, nil)
encoder := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
encCtx := ffmpeg.AVCodecAllocContext3(encoder)
// ... 20+ more API calls ...
```

---

## YouTube Compatibility ✅

| Requirement | ffmpeg-go Support | Status |
|-------------|------------------|--------|
| Container | MP4 | ✅ `libavformat` |
| Video Codec | H.264 | ✅ `libx264` |
| Audio Codec | AAC | ✅ Native FFmpeg AAC |
| Pixel Format | yuv420p | ✅ `libswscale` |
| Resolution | 1280×720 | ✅ Any resolution |
| Frame Rate | 30fps | ✅ Any frame rate |
| Bitrate Control | CRF/CBR | ✅ x264 options |

**Verdict:** Full YouTube upload specification compliance.

---

## Strategic Benefits for Future Development

### Interactive UI/UX Possibilities

With in-process FFmpeg encoding (vs external binary), Jivefire can evolve into a rich interactive tool:

**1. Real-Time Progress & Statistics**
```
┌─ Jivefire ────────────────────────────────────────────────────┐
│ Encoding: podcast-episode-123.mp4                             │
│                                                                │
│ Progress:  [████████████████────────────] 73% (52.1s / 72s)  │
│                                                                │
│ Video:  H.264 @ 4001 kbps  │  Audio:  AAC @ 192 kbps         │
│ FPS:    29.97 (target 30)  │  Bitrate: 4.2 Mbps              │
│ Frames: 1563 / 2160        │  Size:   38.2 MB (est. 52 MB)   │
│                                                                │
│ [P] Pause  [S] Snapshot  [Q] Cancel                           │
└────────────────────────────────────────────────────────────────┘
```

**2. Interactive Parameter Tuning**
- Adjust sensitivity/smoothing during encoding preview
- A/B compare visualizations in real-time
- Hot-reload color schemes without re-encoding

**3. Live Preview Mode**
- Render video in memory buffer
- Scrub timeline to preview different timestamps
- Iterate on visual parameters before final encode

**4. Batch Processing UI**
- Queue multiple episodes with different configs
- Parallel encoding with progress for each
- Pause/resume/reorder queue interactively

**Why External FFmpeg Can't Do This:**
- No access to internal encoder state
- Can't pause/resume encoding programmatically
- Limited to CLI progress parsing (brittle)
- No frame-level control once process started

**Bubbletea Integration:**
```go
// Future possibility with ffmpeg-go
type model struct {
    encoder   *encoder.Encoder
    progress  float64
    stats     encoder.Stats
    paused    bool
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case encoder.ProgressMsg:
        m.progress = msg.Progress
        m.stats = msg.Stats
        return m, waitForProgress
    case tea.KeyMsg:
        if msg.String() == "p" {
            m.encoder.Pause() // ← Only possible with in-process
            m.paused = !m.paused
        }
    }
}
```

**Strategic Value:**
- Positions Jivefire as **interactive podcast video tool**, not just CLI converter
- Enables **Linux Matters branding customization** without code changes
- Differentiates from simple FFmpeg wrappers
- Opens path to **GUI version** if desired (Fyne, Gio)

This architectural benefit alone justifies the migration effort - it's not just about standalone distribution, it's about **enabling a whole category of features** that external FFmpeg makes impossible.

---

## Implementation Considerations

### Current Jivefire Architecture
```
Go Audio Processing → Raw RGB24 frames → FFmpeg stdin → MP4 output
                      (pipe boundary)
```

### With ffmpeg-go
```
Go Audio Processing → Raw RGB24 frames → Go FFmpeg encoding → MP4 output
                      (in-process, no pipe)
```

**Benefits:**
- Eliminates pipe overhead (~5% performance in Jivefire)
- Single-process architecture (easier debugging)
- Direct memory access (no serialization)
- Programmatic encoder control (no CLI parsing)
- **Future UI/UX:** Enables rich interactive experiences (e.g., Bubbletea TUI)
  - Real-time progress bars with encoding statistics
  - Interactive parameter adjustment during encoding
  - Live preview/scrubbing capabilities
  - Impossible with external FFmpeg process

**Costs:**
- More complex encoding code (~200 lines vs ~20 lines)
- Manual frame/packet management
- Memory management responsibility

---

## Migration Effort Estimate

### Code Changes Required

1. **Replace FFmpeg Process** (~200 lines)
   - `AVFormatContext` setup for MP4 muxer
   - `AVCodecContext` setup for H.264 encoder
   - `AVCodecContext` setup for AAC encoder
   - Frame encoding loop
   - Packet muxing

2. **Audio Encoding** (~50 lines)
   - Convert WAV samples to `AVFrame`
   - AAC encoder initialization
   - Audio packet generation

3. **Video Encoding** (~100 lines)
   - RGB24 → YUV420p conversion (existing)
   - `AVFrame` wrapping of pixel data
   - H.264 encoding parameters
   - Frame timestamping

4. **Error Handling** (~50 lines)
   - `WrapErr` for all FFmpeg calls
   - Cleanup/free calls

**Total Estimate:** 1-2 days for experienced FFmpeg developer

**Reference:** Use `examples/transcode/main.go` as template

---

## Performance Analysis

### Current (FFmpeg Process)
- Frame generation: 48.6%
- FFmpeg encoding: 45.8%
- Pipe overhead: ~5%

### Expected (ffmpeg-go)
- Frame generation: 48.6% (unchanged)
- In-process encoding: ~46%
- Pipe overhead: **0%**

**Projected Improvement:** ~5% faster (marginal)

**Key Insight:** Jivefire is already optimal. The benefit is **standalone distribution**, not performance.

---

## Alternatives Considered

### 1. **Keep Current Architecture (FFmpeg CLI)**
- ✅ Simple, battle-tested
- ✅ Minimal code
- ❌ Requires FFmpeg installation
- ❌ Not standalone

### 2. **Pure Go Encoding (h264-go, aac-go)**
- ✅ Pure Go (no CGO)
- ❌ Immature/incomplete implementations
- ❌ Quality concerns for YouTube upload
- ❌ No H.264 high-profile support

### 3. **Different FFmpeg Bindings**
- `github.com/asticode/go-astilibav` - Higher-level abstractions but deprecated
- `github.com/korandiz/v4l` - Linux-specific, camera focus
- None offer pre-built static libraries

**Verdict:** ffmpeg-go is the best available option for standalone binaries.

---

## Licensing Implications

### Current Jivefire
- Pure Go code (presumably BSD/MIT)
- FFmpeg as external runtime dependency (GPL)
- **Distribution:** Separate binaries (no GPL concerns)

### With ffmpeg-go
- ffmpeg-go wrapper: MIT license
- Static FFmpeg build: GPL v3 (due to x264)
- **Distribution:** Single binary = GPL v3 required

**Action Required:**
1. **Relicense Jivefire** to GPL v3 ✅ **ACCEPTABLE** to Linux Matters team
2. Update LICENSE file
3. Add FFmpeg/x264 attribution to README

**Status:** ✅ Licensing is not a blocker for adoption

---

## Recommendations

### ✅ **Adopt ffmpeg-go IF:**
1. **Standalone binary** is high priority for Linux Matters ✅
2. **GPL v3 licensing** is acceptable ✅ **CONFIRMED**
3. Team has **FFmpeg API familiarity** (or willing to learn)
4. **1-2 days development time** is available for migration
5. **Future interactive UI** development is desired ✅ **CONFIRMED**

### ❌ **Keep Current Approach IF:**
1. **Simplicity** is more important than standalone
2. Current **pipe architecture works well** (it does!)

**Update:** With GPL v3 licensing and binary size confirmed as acceptable, **plus the strategic benefit of enabling future interactive UI development** (Bubbletea, real-time controls), ffmpeg-go becomes the recommended path forward.

---

## Migration Roadmap (If Proceeding)

### Phase 1: Proof of Concept (1 day)
1. Add `ffmpeg-go` dependency to go.mod
2. Create `internal/encoder/` package
3. Implement basic H.264 encoder (no audio)
4. Generate test MP4 from single frame
5. Verify playback in VLC/mpv

### Phase 2: Full Integration (1 day)
1. Implement AAC audio encoder
2. Replace `cmd/jivefire/main.go` FFmpeg exec calls
3. Port frame generation loop to `AVFrame` submission
4. Add error handling and cleanup
5. Test with `testdata/dream.wav`

### Phase 3: Testing & Polish (0.5 days)
1. Compare output quality with current version
2. Verify YouTube upload compatibility
3. Update README with new build requirements
4. Document CGO cross-compilation

### Phase 4: Distribution (0.5 days)
1. Setup GitHub Actions for multi-platform builds
2. Pre-compile binaries for releases
3. Update LICENSE to GPL v3
4. Add FFmpeg attribution

**Total Timeline:** 3 days

---

## Conclusion

**ffmpeg-go is technically suitable** for Jivefire's goal of producing YouTube-ready videos in a standalone binary. The project provides:

✅ Complete codec support (H.264, AAC)
✅ Static libraries for all target platforms
✅ Production-ready architecture
✅ Active maintenance
✅ GPL v3 licensing acceptable to Linux Matters team

**Adoption requires:**

⚠️ Tolerating larger binary size (~60MB)
⚠️ Investing 2-3 days in migration effort
⚠️ Managing CGO build complexity

**Decision Point:** Is standalone distribution important enough for Linux Matters to justify the engineering investment?

**My Assessment:** With licensing confirmed as acceptable, **ffmpeg-go is recommended** if standalone distribution ("single binary, no FFmpeg install required") is a priority. The technical capability is proven, and the engineering costs are reasonable for the benefit gained.---

## Questions for Linux Matters Team

1. **Is standalone binary distribution a hard requirement** or a nice-to-have?
2. ~~**Is GPL v3 licensing acceptable** for Jivefire?~~ ✅ **CONFIRMED ACCEPTABLE**
3. **Is ~60MB binary size acceptable** for a desktop video generation tool?
4. **Does the team have FFmpeg API experience**, or would this be a learning curve?
5. **Would 2-3 days development time** be better spent on other Jivefire features?

---

**Evaluator:** GitHub Copilot (AI Assistant for Martin Wimpress)
**Methodology:** Repository analysis, static library inspection, example code review, YouTube specification cross-reference
