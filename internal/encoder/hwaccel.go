package encoder

import (
	"runtime"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// HWAccelType represents a hardware acceleration type
type HWAccelType string

const (
	HWAccelNone         HWAccelType = "none"         // Software encoding (libx264)
	HWAccelAuto         HWAccelType = "auto"         // Auto-detect best available
	HWAccelNVENC        HWAccelType = "nvenc"        // NVIDIA NVENC
	HWAccelQSV          HWAccelType = "qsv"          // Intel Quick Sync Video
	HWAccelVulkan       HWAccelType = "vulkan"       // Vulkan Video
	HWAccelVideoToolbox HWAccelType = "videotoolbox" // Apple VideoToolbox (macOS)
)

// HWEncoder represents a detected hardware encoder
type HWEncoder struct {
	Name        string      // Encoder name (e.g., "h264_nvenc")
	Type        HWAccelType // Hardware acceleration type
	DeviceType  ffmpeg.AVHWDeviceType
	Available   bool   // Whether hardware is present and working
	Description string // Human-readable description
}

// linuxEncoderPriority defines the encoder preference order for Linux
// Priority: nvenc > qsv > vulkan > software
var linuxEncoderPriority = []struct {
	name       string
	accelType  HWAccelType
	deviceType ffmpeg.AVHWDeviceType
	desc       string
}{
	{"h264_nvenc", HWAccelNVENC, ffmpeg.AVHWDeviceTypeCuda, "NVIDIA NVENC"},
	{"h264_qsv", HWAccelQSV, ffmpeg.AVHWDeviceTypeQsv, "Intel Quick Sync Video"},
	{"h264_vulkan", HWAccelVulkan, ffmpeg.AVHWDeviceTypeVulkan, "Vulkan Video"},
}

// macOSEncoderPriority defines the encoder preference order for macOS
// Priority: videotoolbox > software
var macOSEncoderPriority = []struct {
	name       string
	accelType  HWAccelType
	deviceType ffmpeg.AVHWDeviceType
	desc       string
}{
	{"h264_videotoolbox", HWAccelVideoToolbox, ffmpeg.AVHWDeviceTypeVideotoolbox, "Apple VideoToolbox"},
}

// testHardwareAvailable tests if a hardware device type is actually available
// by attempting to create a device context for it.
func testHardwareAvailable(deviceType ffmpeg.AVHWDeviceType) bool {
	// Save current log level and temporarily silence FFmpeg logs
	// to avoid error messages for unavailable hardware
	oldLevel, _ := ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)
	defer ffmpeg.AVLogSetLevel(oldLevel)

	var hwDeviceCtx *ffmpeg.AVBufferRef
	ret, _ := ffmpeg.AVHWDeviceCtxCreate(&hwDeviceCtx, deviceType, nil, nil, 0)
	if ret == 0 && hwDeviceCtx != nil {
		// Successfully created - hardware is available
		ffmpeg.AVBufferUnref(&hwDeviceCtx)
		return true
	}
	return false
}

// DetectHWEncoders probes for available hardware encoders
// Returns a list of detected encoders in priority order
func DetectHWEncoders() []HWEncoder {
	var encoders []HWEncoder

	// Select encoder list based on OS
	var priority []struct {
		name       string
		accelType  HWAccelType
		deviceType ffmpeg.AVHWDeviceType
		desc       string
	}

	switch runtime.GOOS {
	case "darwin":
		priority = macOSEncoderPriority
	default: // Linux and others
		priority = linuxEncoderPriority
	}

	// Check each encoder in priority order
	for _, enc := range priority {
		encoder := HWEncoder{
			Name:        enc.name,
			Type:        enc.accelType,
			DeviceType:  enc.deviceType,
			Description: enc.desc,
			Available:   false,
		}

		// Check if encoder exists in FFmpeg build
		encoderName := ffmpeg.ToCStr(enc.name)
		codec := ffmpeg.AVCodecFindEncoderByName(encoderName)
		encoderName.Free()

		if codec != nil {
			// Encoder exists, now test if hardware is available
			encoder.Available = testHardwareAvailable(enc.deviceType)
		}

		encoders = append(encoders, encoder)
	}

	return encoders
}

// DetectNVENC probes specifically for NVENC availability and returns the encoder if present
func DetectNVENC() *HWEncoder {
	// Look up encoder by name
	name := "h264_nvenc"
	cstr := ffmpeg.ToCStr(name)
	defer cstr.Free()

	codec := ffmpeg.AVCodecFindEncoderByName(cstr)
	if codec == nil {
		return nil
	}

	// Check hardware availability for CUDA device type
	available := testHardwareAvailable(ffmpeg.AVHWDeviceTypeCuda)
	return &HWEncoder{
		Name:        name,
		Type:        HWAccelNVENC,
		DeviceType:  ffmpeg.AVHWDeviceTypeCuda,
		Available:   available,
		Description: "NVIDIA NVENC",
	}
}

// SelectBestEncoder returns the best available encoder based on priority
// If requestedType is HWAccelAuto, it selects the first available hardware encoder
// If requestedType is HWAccelNone, it returns nil (use software)
// Otherwise, it attempts to use the requested type if available
func SelectBestEncoder(requestedType HWAccelType) *HWEncoder {
	if requestedType == HWAccelNone {
		return nil // Explicitly requested software encoding
	}

	// For now we want entirely automatic behaviour on Linux: prefer NVENC, otherwise fall back to software
	if requestedType == HWAccelAuto && runtime.GOOS == "linux" {
		if nv := DetectNVENC(); nv != nil && nv.Available {
			return nv
		}
		return nil
	}

	// Fallback to general detection for other OSes or explicit requests
	encoders := DetectHWEncoders()

	if requestedType == HWAccelAuto {
		// Return first available encoder from the general priority list
		for i := range encoders {
			if encoders[i].Available {
				return &encoders[i]
			}
		}
		return nil // No hardware available
	}

	// Look for specifically requested encoder type
	for i := range encoders {
		if encoders[i].Type == requestedType {
			if encoders[i].Available {
				return &encoders[i]
			}
			return nil // Requested type not available
		}
	}

	return nil // Requested type not found
}

// GetEncoderStatus returns a human-readable status of all hardware encoders
func GetEncoderStatus() string {
	encoders := DetectHWEncoders()

	var sb strings.Builder
	sb.WriteString("Hardware Encoder Status:\n")

	for _, enc := range encoders {
		status := "not available"
		if enc.Available {
			status = "available"
		}
		sb.WriteString("  ")
		sb.WriteString(enc.Description)
		sb.WriteString(" (")
		sb.WriteString(enc.Name)
		sb.WriteString("): ")
		sb.WriteString(status)
		sb.WriteString("\n")
	}

	return sb.String()
}
