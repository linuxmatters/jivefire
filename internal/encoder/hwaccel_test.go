package encoder

import (
	"testing"
)

func TestDetectHWEncoders(t *testing.T) {
	encoders := DetectHWEncoders()

	t.Logf("Detected %d encoder types", len(encoders))

	for _, enc := range encoders {
		status := "not available"
		if enc.Available {
			status = "AVAILABLE"
		}
		t.Logf("  %s (%s): %s", enc.Description, enc.Name, status)
	}
}

func TestSelectBestEncoder(t *testing.T) {
	// Test auto-detection
	enc := SelectBestEncoder(HWAccelAuto)
	if enc != nil {
		t.Logf("Auto-selected encoder: %s (%s)", enc.Description, enc.Name)
	} else {
		t.Log("No hardware encoder available, will use software (libx264)")
	}

	// Test explicit software selection
	enc = SelectBestEncoder(HWAccelNone)
	if enc != nil {
		t.Errorf("Expected nil for HWAccelNone, got %s", enc.Name)
	}
}

func TestGetEncoderStatus(t *testing.T) {
	status := GetEncoderStatus()
	t.Logf("\n%s", status)
}
