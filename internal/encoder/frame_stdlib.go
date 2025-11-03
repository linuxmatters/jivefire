package encoder

import (
	"fmt"
	"image/color"
	"unsafe"

	"github.com/csnewman/ffmpeg-go"
)

// convertRGBToYUVGoStdlib uses Go's actual standard library color conversion
// This is a reference implementation to compare against our optimizations
func convertRGBToYUVGoStdlib(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	if len(rgbData) != width*height*3 {
		return fmt.Errorf("RGB data size mismatch: expected %d, got %d", width*height*3, len(rgbData))
	}

	// Get YUV plane pointers
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	// Process all pixels using Go's standard library
	for y := 0; y < height; y++ {
		yOffset := y * int(yLinesize)
		yPtr := unsafe.Add(unsafe.Pointer(yPlane), yOffset)

		for x := 0; x < width; x++ {
			// Get RGB values
			offset := (y*width + x) * 3
			r := rgbData[offset]
			g := rgbData[offset+1]
			b := rgbData[offset+2]

			// Use Go's standard library for conversion
			yVal, cbVal, crVal := color.RGBToYCbCr(r, g, b)

			// Write Y value
			*(*uint8)(unsafe.Add(yPtr, x)) = yVal

			// Write U and V values (subsampled for 4:2:0)
			if y%2 == 0 && x%2 == 0 {
				uvY := y / 2
				uvX := x / 2

				uOffset := uvY*int(uLinesize) + uvX
				vOffset := uvY*int(vLinesize) + uvX

				*(*uint8)(unsafe.Add(unsafe.Pointer(uPlane), uOffset)) = cbVal
				*(*uint8)(unsafe.Add(unsafe.Pointer(vPlane), vOffset)) = crVal
			}
		}
	}

	return nil
}
