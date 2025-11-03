package encoder

import (
	"fmt"
	"unsafe"

	"github.com/csnewman/ffmpeg-go"
)

// convertRGBToYUV converts RGB24 data to YUV420p format
// This is a naive implementation - Phase 1 POC only
func convertRGBToYUV(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) error {
	// Get pointers to Y, U, V planes using .Get() method
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	// Convert RGB to YUV using standard conversion formulas
	// Y  =  0.299R + 0.587G + 0.114B
	// U  = -0.169R - 0.331G + 0.500B + 128
	// V  =  0.500R - 0.419G - 0.081B + 128

	rgbIdx := 0
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := int(rgbData[rgbIdx])
			g := int(rgbData[rgbIdx+1])
			b := int(rgbData[rgbIdx+2])
			rgbIdx += 3

			// Calculate Y
			yVal := (299*r + 587*g + 114*b) / 1000
			if yVal < 0 {
				yVal = 0
			}
			if yVal > 255 {
				yVal = 255
			}

			// Write Y value
			yOffset := y*int(yLinesize) + x
			yPtr := unsafe.Add(unsafe.Pointer(yPlane), yOffset)
			*(*uint8)(yPtr) = uint8(yVal)

			// U and V are subsampled (one value per 2x2 block)
			if y%2 == 0 && x%2 == 0 {
				uVal := (-169*r-331*g+500*b)/1000 + 128
				vVal := (500*r-419*g-81*b)/1000 + 128

				if uVal < 0 {
					uVal = 0
				}
				if uVal > 255 {
					uVal = 255
				}
				if vVal < 0 {
					vVal = 0
				}
				if vVal > 255 {
					vVal = 255
				}

				uvY := y / 2
				uvX := x / 2

				uOffset := uvY*int(uLinesize) + uvX
				vOffset := uvY*int(vLinesize) + uvX

				uPtr := unsafe.Add(unsafe.Pointer(uPlane), uOffset)
				vPtr := unsafe.Add(unsafe.Pointer(vPlane), vOffset)

				*(*uint8)(uPtr) = uint8(uVal)
				*(*uint8)(vPtr) = uint8(vVal)
			}
		}
	}

	return nil
}

// Helper to validate frame format
func validateFrameFormat(frame *ffmpeg.AVFrame, width, height int) error {
	if frame.Width() != width {
		return fmt.Errorf("frame width mismatch: got %d, expected %d", frame.Width(), width)
	}
	if frame.Height() != height {
		return fmt.Errorf("frame height mismatch: got %d, expected %d", frame.Height(), height)
	}
	if frame.Format() != int(ffmpeg.AVPixFmtYuv420P) {
		return fmt.Errorf("frame format mismatch: expected YUV420P")
	}
	return nil
}
