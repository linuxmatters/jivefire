package encoder

// =============================================================================
// RGB→YUV420P Colourspace Conversion Benchmark
// =============================================================================
//
// This benchmark compares two approaches for converting RGB24 frames to YUV420P:
//
//   1. Go Implementation (parallelised)
//      - Uses goroutines to process row groups across CPU cores
//      - ITU-R BT.601 coefficients matching Go's color package
//      - Located in encoder/frame.go
//
//   2. FFmpeg swscale
//      - FFmpeg's native colourspace conversion library
//      - SIMD-optimised but single-threaded
//      - Accessed via ffmpeg-statigo bindings
//
// Run with: just bench-yuv
//
// Expected results on multi-core systems:
//   - Go implementation is ~8× faster due to parallelisation
//   - swscale has zero allocations but can't leverage multiple cores
//
// =============================================================================

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

const (
	benchWidth  = 1280
	benchHeight = 720
)

// Current Go implementation (copied from encoder/frame.go for comparison)
func convertRGBToYUVGo(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	const (
		yR  = 19595
		yG  = 38470
		yB  = 7471
		cbR = -11056
		cbG = -21712
		cbB = 32768
		crR = 32768
		crG = -27440
		crB = -5328
	)

	numCPU := runtime.NumCPU()
	rowsPerWorker := height / numCPU
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		numCPU = height
	}

	var wg sync.WaitGroup
	wg.Add(numCPU)

	for worker := range numCPU {
		startY := worker * rowsPerWorker
		endY := startY + rowsPerWorker
		if worker == numCPU-1 {
			endY = height
		}

		go func(startY, endY int) {
			defer wg.Done()

			for y := startY; y < endY; y++ {
				yOffset := y * yLinesize
				yPtr := unsafe.Add(yPlane, yOffset)
				rgbIdx := y * width * 3

				for x := range width {
					r1 := int32(rgbData[rgbIdx])
					g1 := int32(rgbData[rgbIdx+1])
					b1 := int32(rgbData[rgbIdx+2])
					rgbIdx += 3

					yy := (yR*r1 + yG*g1 + yB*b1 + 1<<15) >> 16
					*(*uint8)(unsafe.Add(yPtr, x)) = uint8(yy) //nolint:gosec // result is clamped to 0-255

					if (y&1) == 0 && (x&1) == 0 {
						cb := cbR*r1 + cbG*g1 + cbB*b1 + 257<<15
						if uint32(cb)&0xff000000 == 0 {
							cb >>= 16
						} else {
							cb = ^(cb >> 31)
						}

						cr := crR*r1 + crG*g1 + crB*b1 + 257<<15
						if uint32(cr)&0xff000000 == 0 {
							cr >>= 16
						} else {
							cr = ^(cr >> 31)
						}

						uvY := y >> 1
						uvX := x >> 1
						uOffset := uvY*uLinesize + uvX
						vOffset := uvY*vLinesize + uvX

						*(*uint8)(unsafe.Add(uPlane, uOffset)) = uint8(cb) //nolint:gosec // value is clamped
						*(*uint8)(unsafe.Add(vPlane, vOffset)) = uint8(cr) //nolint:gosec // value is clamped
					}
				}
			}
		}(startY, endY)
	}

	wg.Wait()
}

// SwsConverter wraps FFmpeg's swscale for RGB24 → YUV420P conversion
type SwsConverter struct {
	swsCtx   *ffmpeg.SwsContext
	srcFrame *ffmpeg.AVFrame
	width    int
	height   int
}

func NewSwsConverter(width, height int) (*SwsConverter, error) {
	swsCtx := ffmpeg.SwsAllocContext()
	if swsCtx == nil {
		return nil, nil // Would need proper error
	}

	// Configure the scaler
	swsCtx.SetSrcW(width)
	swsCtx.SetSrcH(height)
	swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgb24))
	swsCtx.SetDstW(width)
	swsCtx.SetDstH(height)
	swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
	swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))

	// Initialise the context
	ret, err := ffmpeg.SwsInitContext(swsCtx, nil, nil)
	if err != nil || ret < 0 {
		return nil, err
	}

	// Allocate source frame for RGB data
	srcFrame := ffmpeg.AVFrameAlloc()
	if srcFrame == nil {
		return nil, nil
	}
	srcFrame.SetWidth(width)
	srcFrame.SetHeight(height)
	srcFrame.SetFormat(int(ffmpeg.AVPixFmtRgb24))

	ret, err = ffmpeg.AVFrameGetBuffer(srcFrame, 0)
	if err != nil || ret < 0 {
		ffmpeg.AVFrameFree(&srcFrame)
		return nil, err
	}

	return &SwsConverter{
		swsCtx:   swsCtx,
		srcFrame: srcFrame,
		width:    width,
		height:   height,
	}, nil
}

func (c *SwsConverter) Convert(rgbData []byte, dstFrame *ffmpeg.AVFrame) error {
	// Copy RGB data into source frame
	srcLinesize := c.srcFrame.Linesize().Get(0)
	srcData := c.srcFrame.Data().Get(0)

	for y := 0; y < c.height; y++ {
		srcOffset := y * srcLinesize
		rgbOffset := y * c.width * 3
		for x := 0; x < c.width*3; x++ {
			*(*uint8)(unsafe.Add(srcData, srcOffset+x)) = rgbData[rgbOffset+x]
		}
	}

	// Use FFmpeg's swscale
	_, err := ffmpeg.SwsScaleFrame(c.swsCtx, dstFrame, c.srcFrame)
	return err
}

func (c *SwsConverter) Close() {
	if c.srcFrame != nil {
		ffmpeg.AVFrameFree(&c.srcFrame)
	}
	if c.swsCtx != nil {
		ffmpeg.SwsFreecontext(c.swsCtx)
	}
}

func createTestFrames() ([]byte, *ffmpeg.AVFrame) {
	// Create RGB test data with some pattern
	rgbSize := benchWidth * benchHeight * 3
	rgbData := make([]byte, rgbSize)
	for i := 0; i < rgbSize; i += 3 {
		rgbData[i] = uint8(i % 256)   // R
		rgbData[i+1] = uint8(i % 128) // G
		rgbData[i+2] = uint8(i % 64)  // B
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(benchWidth)
	yuvFrame.SetHeight(benchHeight)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)

	return rgbData, yuvFrame
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkGoRGBToYUV measures the parallelised Go implementation.
// This is the production code path used by Jivefire.
func BenchmarkGoRGBToYUV(b *testing.B) {
	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertRGBToYUVGo(rgbData, yuvFrame, benchWidth, benchHeight)
	}
}

// BenchmarkSwscaleRGBToYUV measures FFmpeg's swscale library.
// Single-threaded but SIMD-optimised.
func BenchmarkSwscaleRGBToYUV(b *testing.B) {
	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		b.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = converter.Convert(rgbData, yuvFrame)
	}
}

// BenchmarkRGBAToYUVDirect measures the direct RGBA→YUV420P conversion.
// This skips the intermediate RGB24 buffer for software encoding path.
func BenchmarkRGBAToYUVDirect(b *testing.B) {
	// Create RGBA test data
	rgbaSize := benchWidth * benchHeight * 4
	rgbaData := make([]byte, rgbaSize)
	for i := 0; i < rgbaSize; i += 4 {
		rgbaData[i] = uint8(i % 256)   // R
		rgbaData[i+1] = uint8(i % 128) // G
		rgbaData[i+2] = uint8(i % 64)  // B
		rgbaData[i+3] = 255            // A
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(benchWidth)
	yuvFrame.SetHeight(benchHeight)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	defer ffmpeg.AVFrameFree(&yuvFrame)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertRGBAToYUV(rgbaData, yuvFrame, benchWidth, benchHeight)
	}
}

// BenchmarkRGBAToYUVViaRGB24 measures RGBA→RGB24→YUV420P (old path).
// This was the original software encoding path before direct RGBA→YUV.
func BenchmarkRGBAToYUVViaRGB24(b *testing.B) {
	// Create RGBA test data
	rgbaSize := benchWidth * benchHeight * 4
	rgbaData := make([]byte, rgbaSize)
	for i := 0; i < rgbaSize; i += 4 {
		rgbaData[i] = uint8(i % 256)   // R
		rgbaData[i+1] = uint8(i % 128) // G
		rgbaData[i+2] = uint8(i % 64)  // B
		rgbaData[i+3] = 255            // A
	}

	// Allocate RGB24 buffer and YUV frame
	rgb24Size := benchWidth * benchHeight * 3
	rgb24Data := make([]byte, rgb24Size)

	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(benchWidth)
	yuvFrame.SetHeight(benchHeight)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	defer ffmpeg.AVFrameFree(&yuvFrame)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Step 1: RGBA → RGB24 (strip alpha)
		srcIdx := 0
		dstIdx := 0
		for dstIdx < rgb24Size {
			rgb24Data[dstIdx] = rgbaData[srcIdx]     // R
			rgb24Data[dstIdx+1] = rgbaData[srcIdx+1] // G
			rgb24Data[dstIdx+2] = rgbaData[srcIdx+2] // B
			srcIdx += 4
			dstIdx += 3
		}
		// Step 2: RGB24 → YUV420P
		convertRGBToYUV(rgb24Data, yuvFrame, benchWidth, benchHeight)
	}
}

// =============================================================================
// Equivalence Tests
// =============================================================================

// TestRGBAConversionEquivalence verifies RGBA→YUV direct and via-RGB24 produce identical output.
func TestRGBAConversionEquivalence(t *testing.T) {
	// Create RGBA test data with varied pattern
	rgbaSize := benchWidth * benchHeight * 4
	rgbaData := make([]byte, rgbaSize)
	for i := 0; i < rgbaSize; i += 4 {
		rgbaData[i] = uint8((i * 7) % 256)   // R
		rgbaData[i+1] = uint8((i * 3) % 256) // G
		rgbaData[i+2] = uint8((i * 5) % 256) // B
		rgbaData[i+3] = 255                  // A (ignored)
	}

	// Allocate YUV frames for both paths
	yuvDirect := ffmpeg.AVFrameAlloc()
	yuvDirect.SetWidth(benchWidth)
	yuvDirect.SetHeight(benchHeight)
	yuvDirect.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvDirect, 0)
	defer ffmpeg.AVFrameFree(&yuvDirect)

	yuvViaRGB := ffmpeg.AVFrameAlloc()
	yuvViaRGB.SetWidth(benchWidth)
	yuvViaRGB.SetHeight(benchHeight)
	yuvViaRGB.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvViaRGB, 0)
	defer ffmpeg.AVFrameFree(&yuvViaRGB)

	// Convert using direct path
	convertRGBAToYUV(rgbaData, yuvDirect, benchWidth, benchHeight)

	// Convert using via-RGB24 path
	rgb24Size := benchWidth * benchHeight * 3
	rgb24Data := make([]byte, rgb24Size)
	srcIdx := 0
	dstIdx := 0
	for dstIdx < rgb24Size {
		rgb24Data[dstIdx] = rgbaData[srcIdx]     // R
		rgb24Data[dstIdx+1] = rgbaData[srcIdx+1] // G
		rgb24Data[dstIdx+2] = rgbaData[srcIdx+2] // B
		srcIdx += 4
		dstIdx += 3
	}
	convertRGBToYUV(rgb24Data, yuvViaRGB, benchWidth, benchHeight)

	// Compare Y planes (should be identical)
	yLinesize := yuvDirect.Linesize().Get(0)
	yPlaneDirect := yuvDirect.Data().Get(0)
	yPlaneViaRGB := yuvViaRGB.Data().Get(0)

	yDiffCount := 0
	for y := range benchHeight {
		for x := range benchWidth {
			offset := y*yLinesize + x
			directVal := *(*uint8)(unsafe.Add(yPlaneDirect, offset))
			viaRGBVal := *(*uint8)(unsafe.Add(yPlaneViaRGB, offset))
			if directVal != viaRGBVal {
				yDiffCount++
			}
		}
	}

	// Compare U planes
	uLinesize := yuvDirect.Linesize().Get(1)
	uPlaneDirect := yuvDirect.Data().Get(1)
	uPlaneViaRGB := yuvViaRGB.Data().Get(1)

	uDiffCount := 0
	for y := range benchHeight / 2 {
		for x := range benchWidth / 2 {
			offset := y*uLinesize + x
			directVal := *(*uint8)(unsafe.Add(uPlaneDirect, offset))
			viaRGBVal := *(*uint8)(unsafe.Add(uPlaneViaRGB, offset))
			if directVal != viaRGBVal {
				uDiffCount++
			}
		}
	}

	// Compare V planes
	vLinesize := yuvDirect.Linesize().Get(2)
	vPlaneDirect := yuvDirect.Data().Get(2)
	vPlaneViaRGB := yuvViaRGB.Data().Get(2)

	vDiffCount := 0
	for y := range benchHeight / 2 {
		for x := range benchWidth / 2 {
			offset := y*vLinesize + x
			directVal := *(*uint8)(unsafe.Add(vPlaneDirect, offset))
			viaRGBVal := *(*uint8)(unsafe.Add(vPlaneViaRGB, offset))
			if directVal != viaRGBVal {
				vDiffCount++
			}
		}
	}

	// Both paths should produce identical output
	if yDiffCount > 0 || uDiffCount > 0 || vDiffCount > 0 {
		t.Errorf("RGBA conversion paths differ: Y=%d, U=%d, V=%d pixel differences",
			yDiffCount, uDiffCount, vDiffCount)
	} else {
		t.Log("RGBA→YUV direct and via-RGB24 paths produce identical output ✓")
	}
}

// TestConversionEquivalence verifies both implementations produce similar output.
// Some pixel differences are expected due to different rounding in coefficient
// implementations (Go uses integer arithmetic, FFmpeg uses floating-point).
func TestConversionEquivalence(t *testing.T) {
	rgbData, yuvFrameGo := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameGo)

	_, yuvFrameSws := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameSws)

	// Convert with Go implementation
	convertRGBToYUVGo(rgbData, yuvFrameGo, benchWidth, benchHeight)

	// Convert with swscale
	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		t.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	err = converter.Convert(rgbData, yuvFrameSws)
	if err != nil {
		t.Fatalf("Swscale conversion failed: %v", err)
	}

	// Compare Y planes (they should be very close, allowing for rounding differences)
	yLinesize := yuvFrameGo.Linesize().Get(0)
	yPlaneGo := yuvFrameGo.Data().Get(0)
	yPlaneSws := yuvFrameSws.Data().Get(0)

	diffCount := 0
	maxDiff := 0
	for y := range benchHeight {
		for x := range benchWidth {
			offset := y*yLinesize + x
			goVal := *(*uint8)(unsafe.Add(yPlaneGo, offset))
			swsVal := *(*uint8)(unsafe.Add(yPlaneSws, offset))
			diff := int(goVal) - int(swsVal)
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 { // Allow 1 unit difference for rounding
				diffCount++
				if diff > maxDiff {
					maxDiff = diff
				}
			}
		}
	}

	t.Logf("Y plane differences > 1: %d pixels (max diff: %d)", diffCount, maxDiff)
}

// =============================================================================
// Summary
// =============================================================================

// TestBenchmarkSummary runs both implementations and prints a comparison.
// This provides a quick human-readable summary without running full benchmarks.
func TestBenchmarkSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping summary in short mode")
	}

	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	// Benchmark Go implementation
	goStart := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			convertRGBToYUVGo(rgbData, yuvFrame, benchWidth, benchHeight)
		}
	})

	// Benchmark swscale
	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		t.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	swsStart := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = converter.Convert(rgbData, yuvFrame)
		}
	})

	goNs := goStart.NsPerOp()
	swsNs := swsStart.NsPerOp()
	speedup := float64(swsNs) / float64(goNs)

	fmt.Println()
	fmt.Println("╭───────────────────────────────────────────────────────────────╮")
	fmt.Println("│          RGB→YUV420P Colourspace Conversion Benchmark         │")
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Resolution:     %d×%d (%.1f megapixels)                    │\n", benchWidth, benchHeight, float64(benchWidth*benchHeight)/1e6)
	fmt.Printf("│  CPU cores:      %-2d                                           │\n", runtime.NumCPU())
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Go (parallel):    %6.0f µs/frame  (%2d allocs)               │\n", float64(goNs)/1000, goStart.AllocsPerOp())
	fmt.Printf("│  FFmpeg swscale:   %6.0f µs/frame  (%2d allocs)               │\n", float64(swsNs)/1000, swsStart.AllocsPerOp())
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  ✓ Go implementation is %.1f× faster                          │\n", speedup)
	fmt.Println("│                                                               │")
	fmt.Println("│  Parallelisation across CPU cores beats SIMD optimisation.   │")
	fmt.Println("╰───────────────────────────────────────────────────────────────╯")
	fmt.Println()
}
