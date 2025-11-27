package encoder

import (
	"runtime"
	"sync"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// convertRGBToYUV converts RGB24 data to YUV420P format using Go's standard library coefficients
// with optimized parallel processing. This version achieves the best performance by combining
// Go's proven color conversion math with efficient parallelization.
func convertRGBToYUV(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	// Get pointers to Y, U, V planes
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	// Use Go's exact coefficients from color/ycbcr.go
	// These are chosen so they sum to exactly 65536 for Y calculation
	const (
		// Y coefficients (sum = 65536)
		yR = 19595 // 0.299 * 65536 (rounded)
		yG = 38470 // 0.587 * 65536 (rounded)
		yB = 7471  // 0.114 * 65536 (rounded)

		// Cb coefficients (sum = 0)
		cbR = -11056 // -0.16874 * 65536 (rounded)
		cbG = -21712 // -0.33126 * 65536 (rounded)
		cbB = 32768  //  0.50000 * 65536

		// Cr coefficients (sum = 0)
		crR = 32768  //  0.50000 * 65536
		crG = -27440 // -0.41869 * 65536 (rounded)
		crB = -5328  // -0.08131 * 65536 (rounded)
	)

	// Parallelize by row groups
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

			for y := startY; y < endY; y++ {
				yOffset := y * int(yLinesize)
				yPtr := unsafe.Add(unsafe.Pointer(yPlane), yOffset)

				rgbIdx := y * width * 3

				for x := 0; x < width; x++ {
					r1 := int32(rgbData[rgbIdx])
					g1 := int32(rgbData[rgbIdx+1])
					b1 := int32(rgbData[rgbIdx+2])
					rgbIdx += 3

					// Y calculation with rounding adjustment
					// Note: 19595 + 38470 + 7471 = 65536 exactly
					yy := (yR*r1 + yG*g1 + yB*b1 + 1<<15) >> 16

					// Write Y value directly
					*(*uint8)(unsafe.Add(yPtr, x)) = uint8(yy)

					// Handle U and V for 4:2:0 subsampling
					if (y&1) == 0 && (x&1) == 0 {
						// Cb calculation with Go's branchless clamping
						// Note: -11056 - 21712 + 32768 = 0
						cb := cbR*r1 + cbG*g1 + cbB*b1 + 257<<15
						if uint32(cb)&0xff000000 == 0 {
							cb >>= 16
						} else {
							cb = ^(cb >> 31)
						}

						// Cr calculation with Go's branchless clamping
						// Note: 32768 - 27440 - 5328 = 0
						cr := crR*r1 + crG*g1 + crB*b1 + 257<<15
						if uint32(cr)&0xff000000 == 0 {
							cr >>= 16
						} else {
							cr = ^(cr >> 31)
						}

						// Write U and V values
						uvY := y >> 1
						uvX := x >> 1

						uOffset := uvY*int(uLinesize) + uvX
						vOffset := uvY*int(vLinesize) + uvX

						*(*uint8)(unsafe.Add(unsafe.Pointer(uPlane), uOffset)) = uint8(cb)
						*(*uint8)(unsafe.Add(unsafe.Pointer(vPlane), vOffset)) = uint8(cr)
					}
				}
			}
		}(startY, endY)
	}

	wg.Wait()
	return nil
}
