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
    rm -fv jivefire

vhs: build
    vhs ./jivefire.tape

# Render full video from dream.wav to test.mp4
video: build
    ./jivefire --episode 42 --title "Testing Testing" testdata/dream.wav testdata/test.mp4

# Run tests
test:
    go test ./...
