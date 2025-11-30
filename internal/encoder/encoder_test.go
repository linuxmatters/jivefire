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
		HWAccel:    HWAccelNone, // Force software encoding for WriteFrame (RGB24→YUV420P)
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

// TestEncoderRGBA tests the RGBA frame writing path
func TestEncoderRGBA(t *testing.T) {
	outputPath := "../../testdata/poc-rgba-video.mp4"
	defer os.Remove(outputPath) // Clean up after test

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

	// Create a single frame with red color (RGBA format)
	frameSize := config.Width * config.Height * 4 // RGBA = 4 bytes per pixel
	redFrame := make([]byte, frameSize)
	for i := 0; i < frameSize; i += 4 {
		redFrame[i] = 255   // R = 255 (red)
		redFrame[i+1] = 0   // G = 0
		redFrame[i+2] = 0   // B = 0
		redFrame[i+3] = 255 // A = 255 (opaque)
	}

	// Write one frame using RGBA path
	err = enc.WriteFrameRGBA(redFrame)
	if err != nil {
		t.Fatalf("Failed to write RGBA frame: %v", err)
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

	t.Logf("Successfully created RGBA video: %s (%d bytes)", outputPath, info.Size())
}
