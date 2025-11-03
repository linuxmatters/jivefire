package encoder

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/csnewman/ffmpeg-go"
)

// convertRGBToYUVOptimized converts RGB24 data to YUV420p format with performance optimizations
func convertRGBToYUVOptimized(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	// Get pointers to Y, U, V planes
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	// Pre-calculate constants to avoid repeated division
	const (
		// Scaled integer coefficients (multiplied by 65536 for precision)
		yr = 19595 // 0.299 * 65536
		yg = 38470 // 0.587 * 65536
		yb = 7471  // 0.114 * 65536

		ur = -11076 // -0.169 * 65536
		ug = -21692 // -0.331 * 65536
		ub = 32768  // 0.500 * 65536

		vr = 32768  // 0.500 * 65536
		vg = -27460 // -0.419 * 65536
		vb = -5308  // -0.081 * 65536
	)

	// Use goroutines to parallelize the conversion
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

			// Process Y values and collect UV samples
			for y := startY; y < endY; y++ {
				yOffset := y * int(yLinesize)
				yPtr := unsafe.Add(unsafe.Pointer(yPlane), yOffset)

				rgbIdx := y * width * 3

				// Process entire row for Y channel
				for x := 0; x < width; x++ {
					r := int(rgbData[rgbIdx])
					g := int(rgbData[rgbIdx+1])
					b := int(rgbData[rgbIdx+2])
					rgbIdx += 3

					// Calculate Y using fixed-point arithmetic
					yVal := (yr*r + yg*g + yb*b) >> 16

					// Write Y value directly without bounds checking
					*(*uint8)(unsafe.Add(yPtr, x)) = uint8(yVal)

					// Handle U and V for every other pixel in every other row
					if (y&1) == 0 && (x&1) == 0 {
						// Calculate U and V
						uVal := ((ur*r + ug*g + ub*b) >> 16) + 128
						vVal := ((vr*r + vg*g + vb*b) >> 16) + 128

						// Write U and V values
						uvY := y >> 1
						uvX := x >> 1

						uOffset := uvY*int(uLinesize) + uvX
						vOffset := uvY*int(vLinesize) + uvX

						*(*uint8)(unsafe.Add(unsafe.Pointer(uPlane), uOffset)) = uint8(uVal)
						*(*uint8)(unsafe.Add(unsafe.Pointer(vPlane), vOffset)) = uint8(vVal)
					}
				}
			}
		}(startY, endY)
	}

	wg.Wait()
	return nil
}

// Alternative: Table-based conversion for even better performance
var (
	yTableR [256]int
	yTableG [256]int
	yTableB [256]int
	uTableR [256]int
	uTableG [256]int
	uTableB [256]int
	vTableR [256]int
	vTableG [256]int
	vTableB [256]int

	tablesInitialized bool
	tablesMutex       sync.Mutex
)

func initTables() {
	tablesMutex.Lock()
	defer tablesMutex.Unlock()

	if tablesInitialized {
		return
	}

	// Pre-calculate all possible values
	for i := 0; i < 256; i++ {
		yTableR[i] = (299 * i) / 1000
		yTableG[i] = (587 * i) / 1000
		yTableB[i] = (114 * i) / 1000

		uTableR[i] = (-169 * i) / 1000
		uTableG[i] = (-331 * i) / 1000
		uTableB[i] = (500 * i) / 1000

		vTableR[i] = (500 * i) / 1000
		vTableG[i] = (-419 * i) / 1000
		vTableB[i] = (-81 * i) / 1000
	}

	tablesInitialized = true
}

// convertRGBToYUVTable uses lookup tables for fastest conversion
func convertRGBToYUVTable(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	initTables()

	// Get pointers to Y, U, V planes
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	rgbIdx := 0
	for y := 0; y < height; y++ {
		yOffset := y * int(yLinesize)
		yPtr := unsafe.Add(unsafe.Pointer(yPlane), yOffset)

		for x := 0; x < width; x++ {
			r := rgbData[rgbIdx]
			g := rgbData[rgbIdx+1]
			b := rgbData[rgbIdx+2]
			rgbIdx += 3

			// Y value from lookup tables
			yVal := yTableR[r] + yTableG[g] + yTableB[b]
			*(*uint8)(unsafe.Add(yPtr, x)) = uint8(yVal)

			// U and V for every other pixel in every other row
			if (y&1) == 0 && (x&1) == 0 {
				uVal := uTableR[r] + uTableG[g] + uTableB[b] + 128
				vVal := vTableR[r] + vTableG[g] + vTableB[b] + 128

				uvY := y >> 1
				uvX := x >> 1

				uOffset := uvY*int(uLinesize) + uvX
				vOffset := uvY*int(vLinesize) + uvX

				*(*uint8)(unsafe.Add(unsafe.Pointer(uPlane), uOffset)) = uint8(uVal)
				*(*uint8)(unsafe.Add(unsafe.Pointer(vPlane), vOffset)) = uint8(vVal)
			}
		}
	}

	return nil
}
