# Audio Visualizer - Just Commands

# Default recipe (shows available commands)
default:
    @just --list

# Build the visualizer binary
build:
    go build -o visualizer ./cmd/visualizer

# Generate a snapshot at 10 second mark
snapshot: build
    ./visualizer --snapshot=10.0 testdata/dream.wav testdata/snapshot.png
    @echo "✓ Snapshot saved to testdata/snapshot.png"

# Render full video from dream.wav to test.mp4
video: build
    ./visualizer testdata/dream.wav testdata/test.mp4
    @echo "✓ Video saved to testdata/test.mp4"

# Clean build artifacts
clean:
    rm -f visualizer
    @echo "✓ Cleaned build artifacts"

# Run with custom input/output
run INPUT OUTPUT: build
    ./visualizer {{INPUT}} {{OUTPUT}}

# Run tests
test:
    go test ./...
