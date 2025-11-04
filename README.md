# Jivefire 🔥

> Spin your podcast .wav into a groovy MP4 visualiser. [Cava](https://github.com/karlstav/cava)-inspired real-time audio frequencies.

## The Groove

64 discrete bars. Symmetric mirroring. Silky smooth decay animation.

## Usage

### Generate Video
```bash
./jivefire input.wav output.mp4
```

### With Episode Number and Title
```bash
./jivefire --episode=42 --title="Linux Matters" input.wav output.mp4
```

<div align="center"><img alt="Jivefire Demo" src=".github/jivefire.gif" width="860" /></div>

## What You Get

- **64 discrete frequency bars**
- **Symmetric mirroring**
- **1280×720 @ 30fps** H.264 MP4
- **FFT-based** (2048-point, Hanning window, log scale)
- **Smooth decay**

## Build

```bash
just build      # Build binary
just video      # Render test audio

# Manual
go build -o jivefire ./cmd/jivefire
```

---

**Project Context:** Linux Matters podcast video production tool. Discretizes audio into visual groove for YouTube.
