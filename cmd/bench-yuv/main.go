// bench-yuv is a standalone benchmark for RGB→YUV colour space conversion.
// Designed to be called by hyperfine for statistical analysis.
//
// Usage:
//
//	bench-yuv [--iterations N] [--impl go|swscale]
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

const (
	width  = 1280
	height = 720
)

// YCbCr coefficients (BT.601)
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

func rgbToY(r, g, b int32) uint8 {
	return uint8((yR*r + yG*g + yB*b + 1<<15) >> 16)
}

func rgbToCb(r, g, b int32) uint8 {
	cb := cbR*r + cbG*g + cbB*b + 257<<15
	if uint32(cb)&0xff000000 == 0 {
		cb >>= 16
	} else {
		cb = ^(cb >> 31)
	}
	return uint8(cb)
}

func rgbToCr(r, g, b int32) uint8 {
	cr := crR*r + crG*g + crB*b + 257<<15
	if uint32(cr)&0xff000000 == 0 {
		cr >>= 16
	} else {
		cr = ^(cr >> 31)
	}
	return uint8(cr)
}

func parallelRows(height int, fn func(startY, endY int)) {
	numCPU := runtime.NumCPU()
	rowsPerWorker := height / numCPU
	if rowsPerWorker < 1 {
		rowsPerWorker = 1
		numCPU = height
	}

	var wg sync.WaitGroup
	wg.Add(numCPU)

	for worker := 0; worker < numCPU; worker++ {
		startY := worker * rowsPerWorker
		endY := startY + rowsPerWorker
		if worker == numCPU-1 {
			endY = height
		}

		go func(startY, endY int) {
			defer wg.Done()
			fn(startY, endY)
		}(startY, endY)
	}

	wg.Wait()
}

func convertRGBToYUVGo(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := int(yuvFrame.Linesize().Get(0))
	uLinesize := int(yuvFrame.Linesize().Get(1))
	vLinesize := int(yuvFrame.Linesize().Get(2))

	parallelRows(height, func(startY, endY int) {
		evenStart := startY
		if evenStart&1 != 0 {
			evenStart++
		}

		// Process even rows: Y + UV
		for y := evenStart; y < endY; y += 2 {
			yPtr := unsafe.Add(unsafe.Pointer(yPlane), y*yLinesize)
			uvY := y >> 1
			uRowPtr := unsafe.Add(unsafe.Pointer(uPlane), uvY*uLinesize)
			vRowPtr := unsafe.Add(unsafe.Pointer(vPlane), uvY*vLinesize)
			rgbIdx := y * width * 3

			for x := 0; x < width; x++ {
				r := int32(rgbData[rgbIdx])
				g := int32(rgbData[rgbIdx+1])
				b := int32(rgbData[rgbIdx+2])
				rgbIdx += 3

				*(*uint8)(unsafe.Add(yPtr, x)) = rgbToY(r, g, b)

				if (x & 1) == 0 {
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uRowPtr, uvX)) = rgbToCb(r, g, b)
					*(*uint8)(unsafe.Add(vRowPtr, uvX)) = rgbToCr(r, g, b)
				}
			}
		}

		// Process odd rows: Y only
		oddStart := startY
		if oddStart&1 == 0 {
			oddStart++
		}
		for y := oddStart; y < endY; y += 2 {
			yPtr := unsafe.Add(unsafe.Pointer(yPlane), y*yLinesize)
			rgbIdx := y * width * 3

			for x := 0; x < width; x++ {
				r := int32(rgbData[rgbIdx])
				g := int32(rgbData[rgbIdx+1])
				b := int32(rgbData[rgbIdx+2])
				rgbIdx += 3

				*(*uint8)(unsafe.Add(yPtr, x)) = rgbToY(r, g, b)
			}
		}
	})
}

func convertSwscale(rgbData []byte, yuvFrame *ffmpeg.AVFrame, swsCtx *ffmpeg.SwsContext, srcFrame *ffmpeg.AVFrame, width, height int) {
	// Copy RGB data into source frame
	srcLinesize := srcFrame.Linesize().Get(0)
	srcData := srcFrame.Data().Get(0)

	for y := 0; y < height; y++ {
		srcOffset := y * int(srcLinesize)
		rgbOffset := y * width * 3
		for x := 0; x < width*3; x++ {
			*(*uint8)(unsafe.Add(unsafe.Pointer(srcData), srcOffset+x)) = rgbData[rgbOffset+x]
		}
	}

	ffmpeg.SwsScaleFrame(swsCtx, yuvFrame, srcFrame)
}

func main() {
	iterations := flag.Int("iterations", 1000, "number of conversions to perform")
	impl := flag.String("impl", "go", "implementation: go or swscale")
	flag.Parse()

	// Create test RGB data
	rgbSize := width * height * 3
	rgbData := make([]byte, rgbSize)
	for i := 0; i < rgbSize; i += 3 {
		rgbData[i] = uint8(i % 256)
		rgbData[i+1] = uint8(i % 128)
		rgbData[i+2] = uint8(i % 64)
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(width)
	yuvFrame.SetHeight(height)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	defer ffmpeg.AVFrameFree(&yuvFrame)

	switch *impl {
	case "go":
		for i := 0; i < *iterations; i++ {
			convertRGBToYUVGo(rgbData, yuvFrame, width, height)
		}
	case "swscale":
		// Set up swscale context
		swsCtx := ffmpeg.SwsAllocContext()
		swsCtx.SetSrcW(width)
		swsCtx.SetSrcH(height)
		swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgb24))
		swsCtx.SetDstW(width)
		swsCtx.SetDstH(height)
		swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
		swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))
		ffmpeg.SwsInitContext(swsCtx, nil, nil)
		defer ffmpeg.SwsFreecontext(swsCtx)

		srcFrame := ffmpeg.AVFrameAlloc()
		srcFrame.SetWidth(width)
		srcFrame.SetHeight(height)
		srcFrame.SetFormat(int(ffmpeg.AVPixFmtRgb24))
		ffmpeg.AVFrameGetBuffer(srcFrame, 0)
		defer ffmpeg.AVFrameFree(&srcFrame)

		for i := 0; i < *iterations; i++ {
			convertSwscale(rgbData, yuvFrame, swsCtx, srcFrame, width, height)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown implementation: %s (use 'go' or 'swscale')\n", *impl)
		os.Exit(1)
	}
}
