package encoder

import (
	"os"
	"testing"
)

// TestEncoderPOC is a proof-of-concept test that encodes a single black frame
func TestEncoderPOC(t *testing.T) {
	outputPath := "../../testdata/poc-video.mp4"
	// Keep the file for inspection - comment out cleanup
	// defer os.Remove(outputPath)

	config := Config{
		OutputPath: outputPath,
		Width:      1280,
		Height:     720,
		Framerate:  30,
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Initialize()
	if err != nil {
		t.Fatalf("Failed to initialize encoder: %v", err)
	}
	defer enc.Close()

	// Create a single black frame (RGB24 format)
	frameSize := config.Width * config.Height * 3 // RGB24 = 3 bytes per pixel
	blackFrame := make([]byte, frameSize)
	// blackFrame is already all zeros (black)

	// Write one frame
	err = enc.WriteFrame(blackFrame)
	if err != nil {
		t.Fatalf("Failed to write frame: %v", err)
	}

	// Close encoder (writes trailer)
	err = enc.Close()
	if err != nil {
		t.Fatalf("Failed to close encoder: %v", err)
	}

	// Verify output file exists and has non-zero size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}

	t.Logf("Successfully created video: %s (%d bytes)", outputPath, info.Size())
}
