# Jivefire 🔥

> Spin your podcast .wav into a groovy MP4 visualiser. Cava-inspired audio frequencies dancing in real-time.

## The Groove

64 discrete bars. Symmetric mirroring. Silky smooth decay animation. That's Jivefire.

## Usage

### Generate Video
```bash
./jivefire input.wav output.mp4
```

### Snapshot
Get a preview image.

```bash
./jivefire --snapshot=10.0 input.wav frame.png
```

## What You Get

✅ **64 discrete frequency bars**
✅ **Symmetric mirroring**
✅ **1280×720 @ 30fps** H.264 MP4
✅ **FFT-based** (2048-point, Hanning window, log scale)
✅ **Smooth decay**

## Build

```bash
just build      # Build binary
just video      # Render test audio
just snapshot   # Quick 10s frame

# Manual
go build -o jivefire ./cmd/jivefire
```

---

**Project Context:** Linux Matters podcast video production tool. Discretizes audio into visual groove for YouTube.
