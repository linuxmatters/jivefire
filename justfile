# Jivefire v0.0.1 - Just Commands
# Spin your podcast .wav into a groovy MP4 visualiser

# Default recipe (shows available commands)
default:
    @just --list

# Build the jivefire binary
build:
    go build -o jivefire ./cmd/jivefire

# Generate a snapshot at 10 second mark
snapshot: build
    ./jivefire --snapshot=10.0 testdata/dream.wav testdata/snapshot.png
    @echo "✓ Snapshot saved to testdata/snapshot.png"

# Render full video from dream.wav to test.mp4
video: build
    ./jivefire testdata/dream.wav testdata/test.mp4
    @echo "✓ Video saved to testdata/test.mp4"

# Clean build artifacts
clean:
    rm -f jivefire
    @echo "✓ Cleaned build artifacts"

# Run with custom input/output
run INPUT OUTPUT: build
    ./jivefire {{INPUT}} {{OUTPUT}}

# Run tests
test:
    go test ./...
