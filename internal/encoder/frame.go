package encoder

// TODO: When Go 1.26 ships with the simd package, revisit with inlinable AVX2
// intrinsics for potentially 30-50% additional gains in colour space conversion.

import (
	"runtime"
	"sync"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// YCbCr coefficients from Go's color/ycbcr.go (BT.601 standard).
// These are fixed-point values scaled by 65536 for integer arithmetic.
const (
	// Y coefficients (sum = 65536)
	yR = 19595 // 0.299 * 65536
	yG = 38470 // 0.587 * 65536
	yB = 7471  // 0.114 * 65536

	// Cb coefficients (sum = 0)
	cbR = -11056 // -0.16874 * 65536
	cbG = -21712 // -0.33126 * 65536
	cbB = 32768  //  0.50000 * 65536

	// Cr coefficients (sum = 0)
	crR = 32768  //  0.50000 * 65536
	crG = -27440 // -0.41869 * 65536
	crB = -5328  // -0.08131 * 65536
)

// rgbToY converts RGB to Y (luma) component.
//
//go:inline
func rgbToY(r, g, b int32) uint8 {
	return uint8((yR*r + yG*g + yB*b + 1<<15) >> 16)
}

// rgbToCb converts RGB to Cb (blue-difference chroma) with branchless clamping.
//
//go:inline
func rgbToCb(r, g, b int32) uint8 {
	cb := cbR*r + cbG*g + cbB*b + 257<<15
	if uint32(cb)&0xff000000 == 0 {
		cb >>= 16
	} else {
		cb = ^(cb >> 31)
	}
	return uint8(cb)
}

// rgbToCr converts RGB to Cr (red-difference chroma) with branchless clamping.
//
//go:inline
func rgbToCr(r, g, b int32) uint8 {
	cr := crR*r + crG*g + crB*b + 257<<15
	if uint32(cr)&0xff000000 == 0 {
		cr >>= 16
	} else {
		cr = ^(cr >> 31)
	}
	return uint8(cr)
}

// parallelRows executes fn across height rows using all CPU cores.
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

// convertRGBToYUV converts RGB24 data to YUV420P (planar) format.
func convertRGBToYUV(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := int(yuvFrame.Linesize().Get(0))
	uLinesize := int(yuvFrame.Linesize().Get(1))
	vLinesize := int(yuvFrame.Linesize().Get(2))

	parallelRows(height, func(startY, endY int) {
		// Align startY to even for correct UV row calculation
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

				// UV subsampling: every other pixel on even rows
				if (x & 1) == 0 {
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uRowPtr, uvX)) = rgbToCb(r, g, b)
					*(*uint8)(unsafe.Add(vRowPtr, uvX)) = rgbToCr(r, g, b)
				}
			}
		}

		// Process odd rows: Y only (no UV)
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

	return nil
}

// convertRGBAToYUV converts RGBA data directly to YUV420P (planar) format.
// Skips the intermediate RGB24 buffer allocation for significantly faster software encoding.
func convertRGBAToYUV(rgbaData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := int(yuvFrame.Linesize().Get(0))
	uLinesize := int(yuvFrame.Linesize().Get(1))
	vLinesize := int(yuvFrame.Linesize().Get(2))

	parallelRows(height, func(startY, endY int) {
		// Align startY to even for correct UV row calculation
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
			rgbaIdx := y * width * 4

			for x := 0; x < width; x++ {
				r := int32(rgbaData[rgbaIdx])
				g := int32(rgbaData[rgbaIdx+1])
				b := int32(rgbaData[rgbaIdx+2])
				rgbaIdx += 4 // Skip alpha

				*(*uint8)(unsafe.Add(yPtr, x)) = rgbToY(r, g, b)

				// UV subsampling: every other pixel on even rows
				if (x & 1) == 0 {
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uRowPtr, uvX)) = rgbToCb(r, g, b)
					*(*uint8)(unsafe.Add(vRowPtr, uvX)) = rgbToCr(r, g, b)
				}
			}
		}

		// Process odd rows: Y only (no UV)
		oddStart := startY
		if oddStart&1 == 0 {
			oddStart++
		}
		for y := oddStart; y < endY; y += 2 {
			yPtr := unsafe.Add(unsafe.Pointer(yPlane), y*yLinesize)
			rgbaIdx := y * width * 4

			for x := 0; x < width; x++ {
				r := int32(rgbaData[rgbaIdx])
				g := int32(rgbaData[rgbaIdx+1])
				b := int32(rgbaData[rgbaIdx+2])
				rgbaIdx += 4 // Skip alpha

				*(*uint8)(unsafe.Add(yPtr, x)) = rgbToY(r, g, b)
			}
		}
	})

	return nil
}

// convertRGBAToNV12 converts RGBA data to NV12 (semi-planar) format.
// NV12 has a Y plane followed by interleaved UV plane.
func convertRGBAToNV12(rgbaData []byte, nv12Frame *ffmpeg.AVFrame, width, height int) error {
	yPlane := nv12Frame.Data().Get(0)
	uvPlane := nv12Frame.Data().Get(1)

	yLinesize := int(nv12Frame.Linesize().Get(0))
	uvLinesize := int(nv12Frame.Linesize().Get(1))

	parallelRows(height, func(startY, endY int) {
		for y := startY; y < endY; y++ {
			yPtr := unsafe.Add(unsafe.Pointer(yPlane), y*yLinesize)
			rgbaIdx := y * width * 4

			for x := 0; x < width; x++ {
				r := int32(rgbaData[rgbaIdx])
				g := int32(rgbaData[rgbaIdx+1])
				b := int32(rgbaData[rgbaIdx+2])
				rgbaIdx += 4 // Skip alpha

				*(*uint8)(unsafe.Add(yPtr, x)) = rgbToY(r, g, b)

				// UV subsampling: top-left pixel of each 2×2 block
				if (y&1) == 0 && (x&1) == 0 {
					uvY := y >> 1
					uvX := x >> 1
					uvPtr := unsafe.Add(unsafe.Pointer(uvPlane), uvY*uvLinesize+uvX*2)
					*(*uint8)(uvPtr) = rgbToCb(r, g, b)
					*(*uint8)(unsafe.Add(uvPtr, 1)) = rgbToCr(r, g, b)
				}
			}
		}
	})

	return nil
}
