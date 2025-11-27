# Jivefire - Just Commands
# Spin your podcast .wav into a groovy MP4 visualiser

# Default recipe (shows available commands)
default:
    @just --list

# Download ffmpeg-statigo libraries
setup:
    #!/usr/bin/env bash
    echo "Initialising ffmpeg-statigo submodule..."
    git submodule update --init --recursive
    echo "Downloading ffmpeg-statigo libraries..."
    cd vendor/ffmpeg-statigo && go run ./cmd/download-lib
    echo "Setup complete!"

# Update ffmpeg-statigo submodule
update-ffmpeg:
    #!/usr/bin/env bash
    echo "Updating ffmpeg-statigo submodule..."
    cd vendor/ffmpeg-statigo
    git pull origin main
    cd ../..
    git add vendor/ffmpeg-statigo
    echo "Submodule updated"
    just setup
    echo "Don't forget to commit: git commit -m 'chore: update ffmpeg-statigo submodule'"

# Build the jivefire binary (dev version)
build:
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    echo "Building jivefire version: $VERSION"
    CGO_ENABLED=1 go build -mod=mod -ldflags="-X main.version=$VERSION" -o jivefire ./cmd/jivefire

# Install the jivefire binary to ~/.local/bin
install: build
    @mkdir -p ~/.local/bin 2>/dev/null || true
    @mv ./jivefire ~/.local/bin/jivefire
    @echo "Installed jivefire to ~/.local/bin/jivefire"
    @echo "Make sure ~/.local/bin is in your PATH"

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
    ./jivefire --episode="01" --title "Linux Matters flac (mono)" testdata/LMP0.flac testdata/LMP0-flac.mp4
    ./jivefire --episode="02" --title "Linux Matters flac (stereo)" testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4

# Render video from LMP0.mp3
test-mp3: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0-stereo.mp3 ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.mp3
    fi
    ./jivefire --episode="01" --title "Linux Matters mp3 (mono)" testdata/LMP0.mp3 testdata/LMP0-mp3.mp4
    ./jivefire --episode="02" --title "Linux Matters mp3 (stereo)" testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4

# Render video from LMP0.wav
test-wav: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 testdata/LMP0.wav
    fi
    if [ ! -f testdata/LMP0-stereo.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.wav
    fi
    ./jivefire --episode="01" --title "Linux Matters: wav (mono)" testdata/LMP0.wav testdata/LMP0-wav.mp4
    ./jivefire --episode="02" --title "Linux Matters: wav (stereo)" testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4

# Test partial episode 67 with FLAC input
test-67: build
    ./jivefire --episode="67" --title "Test Episode 67" testdata/LMP67.flac testdata/LMP67.mp4

# Test preview performance comparison
test-preview: build
    time ./jivefire --episode="0" --title "Test With Preview" testdata/LMP0.mp3 testdata/LMP0-mp3.mp4
    time ./jivefire --episode="0" --title "Test No Preview" --no-preview testdata/LMP0.mp3 testdata/LMP0-mp3.mp4

# Run tests
test:
    go test -mod=mod ./...
