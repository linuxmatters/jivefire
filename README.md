# Jivefire 🔥

> Spin your podcast .wav into a groovy MP4 visualiser. [Cava](https://github.com/karlstav/cava)-inspired real-time audio frequencies.

## The Groove

Your podcast audio deserves more than a static image on YouTube. Jivefire transforms WAV/MP3/FLAC into delightful 720p visuals—bars that breathe with your dialogue, rise with your laughter, and groove through every frequency.

<div align="center"><img alt="Jivefire Demo" src=".github/jivefire.gif" width="860" /></div>

### What's Cooking


- 🖼️ **Thumbnail generator**—YouTube-style PNG with your title, saved alongside the video
- 🎬 **1280×720 @ 30fps** H.264/AAC MP4—YouTube-ready, no questions asked
  - 🎚️ **64 frequency bars** that actually look discrete (not that smeared spectrum nonsense)
  - 🪞 **Symmetric mirroring** above and below centre—double the visual impact
  - 🔬 **FFT-based analysis** (2048-point Hanning window, log scale frequency binning)
  - ✨ **Smooth decay animation** à la CAVA—bars rise fast, fall gracefully
- 🚀 **Stupidly fast**—streaming pipeline, parallel RGB→YUV, zero bloat
- 📦 **Single binary** No Python. No FFmpeg install required. Just drop and render
  - 🐧 **Linux** (amd64 and aarch64)
  - 🍏 **macOS** (x86 and Apple Silicon)

## Usage

### Generate Video
```bash
./jivefire input.wav output.mp4
```

### With Episode Number and Title
```bash
./jivefire --episode=42 --title="Linux Matters" input.wav output.mp4
```

### Example

<div align="center">
  <a href="https://www.youtube.com/watch?v=VPJEQhdaXrk" target="_blank">
    <img alt="Linux Matters: Episode 65 (macOS Made Me Snap)" src=".github/thumbnail.png" width="640">
  </a>
</div>

## Build

```bash
just build      # Build binary
just test-mp3   # Render test audio

# Manual
go build -o jivefire ./cmd/jivefire
```

## Architecture

The Jivefire architecture, such as it is, is available in the [ARCHITECTURE.md](docs/ARCHITECTURE.md) document.
