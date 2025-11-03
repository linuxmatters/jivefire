package encoder

import (
	"fmt"
	"time"

	ffmpeg "github.com/csnewman/ffmpeg-go"
)

// ProfileConversion helps identify the performance bottleneck
func ProfileConversion(e *Encoder) {
	// Create a test RGB frame
	width, height := 1280, 720
	rgbData := make([]byte, width*height*3)

	// Fill with test pattern
	for i := range rgbData {
		rgbData[i] = byte(i % 256)
	}

	// Allocate YUV frame for testing
	yuvFrame := ffmpeg.AVFrameAlloc()
	if yuvFrame == nil {
		fmt.Println("Failed to allocate YUV frame for benchmarking")
		return
	}
	defer ffmpeg.AVFrameFree(&yuvFrame)

	yuvFrame.SetWidth(width)
	yuvFrame.SetHeight(height)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))

	ret, err := ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	if err != nil || ret < 0 {
		fmt.Printf("Failed to allocate YUV buffer: %v\n", err)
		return
	}

	// Time the original conversion
	start := time.Now()
	for i := 0; i < 100; i++ {
		convertRGBToYUV(rgbData, yuvFrame, width, height)
	}
	originalTime := time.Since(start)

	// Time the optimized conversion
	start = time.Now()
	for i := 0; i < 100; i++ {
		convertRGBToYUVOptimized(rgbData, yuvFrame, width, height)
	}
	optimizedTime := time.Since(start)

	// Time the table-based conversion
	start = time.Now()
	for i := 0; i < 100; i++ {
		convertRGBToYUVTable(rgbData, yuvFrame, width, height)
	}
	tableTime := time.Since(start)

	// Time the actual Go stdlib conversion (no parallelization)
	start = time.Now()
	for i := 0; i < 100; i++ {
		convertRGBToYUVGoStdlib(rgbData, yuvFrame, width, height)
	}
	goStdlibTime := time.Since(start)

	// Time the stdlib-optimized conversion (parallelized with exact coefficients)
	start = time.Now()
	for i := 0; i < 100; i++ {
		convertRGBToYUVStdlibOptimized(rgbData, yuvFrame, width, height)
	}
	stdlibOptTime := time.Since(start)

	fmt.Printf("RGB to YUV Conversion Performance (100 frames):\n")
	fmt.Printf("  Original:      %v (%.2f ms/frame)\n", originalTime, float64(originalTime.Milliseconds())/100)
	fmt.Printf("  Optimized:     %v (%.2f ms/frame)\n", optimizedTime, float64(optimizedTime.Milliseconds())/100)
	fmt.Printf("  Table:         %v (%.2f ms/frame)\n", tableTime, float64(tableTime.Milliseconds())/100)
	fmt.Printf("  Go Stdlib:     %v (%.2f ms/frame)\n", goStdlibTime, float64(goStdlibTime.Milliseconds())/100)
	fmt.Printf("  Stdlib+Opt:    %v (%.2f ms/frame)\n", stdlibOptTime, float64(stdlibOptTime.Milliseconds())/100)
	fmt.Printf("  Speedup:       %.2fx (optimized), %.2fx (table), %.2fx (go-stdlib), %.2fx (stdlib+opt)\n",
		float64(originalTime)/float64(optimizedTime),
		float64(originalTime)/float64(tableTime),
		float64(originalTime)/float64(goStdlibTime),
		float64(originalTime)/float64(stdlibOptTime))
}
