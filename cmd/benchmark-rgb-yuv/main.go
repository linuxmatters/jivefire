package main

import (
	"fmt"
	"log"

	"github.com/linuxmatters/jivefire/internal/encoder"
)

func main() {
	// Create a test encoder for benchmarking
	cfg := &encoder.Config{
		Width:      1280,
		Height:     720,
		Framerate:  30,
		OutputPath: "/tmp/benchmark.mp4",
		AudioPath:  "", // No audio needed for RGB->YUV benchmark
	}

	enc, err := encoder.New(*cfg)
	if err != nil {
		log.Fatalf("Failed to create encoder: %v", err)
	}
	defer enc.Close()

	fmt.Println("Running RGB to YUV conversion benchmarks...")
	fmt.Println("=========================================")
	encoder.ProfileConversion(enc)
}
