# Jivefire v0.0.1 - Just Commands
# Spin your podcast .wav into a groovy MP4 visualiser

# Default recipe (shows available commands)
default:
    @just --list

# Build the jivefire binary
build:
    go build -o jivefire ./cmd/jivefire

# Clean build artifacts
clean:
    rm -fv jivefire 2>/dev/null || true
    @rm testdata/*.mp4 2>/dev/null || true
    @rm testdata/*.flac 2>/dev/null || true
    @rm testdata/*.wav 2>/dev/null || true
    @rm testdata/*-stereo.mp3 2>/dev/null || true

# Make a VHS tape recording
vhs: build
    @vhs ./jivefire.tape

# Render video from LMP0.flac
test-flac: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0.flac ]; then
      ffmpeg -i testdata/LMP0.mp3 testdata/LMP0.flac
    fi
    if [ ! -f testdata/LMP0-stereo.flac ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.flac
    fi
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0.flac testdata/LMP0-flac.mp4
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4

# Render video from LMP0.mp3
test-mp3: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0-stereo.mp3 ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.mp3
    fi
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0.mp3 testdata/LMP0-mp3.mp4
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4

# Render video from LMP0.wav
test-wav: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 testdata/LMP0.wav
    fi
    if [ ! -f testdata/LMP0-stereo.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.wav
    fi
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0.wav testdata/LMP0-wav.mp4
    ./jivefire --episode="0" --title "Introducing Linux Matters Podcast" testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4

# Run tests
test:
    go test ./...
