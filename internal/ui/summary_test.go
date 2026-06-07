package ui

import (
	"strings"
	"testing"
	"time"
)

// TestRenderCompleteUsesTable verifies the completion summary renders through
// lipgloss/table rather than the old %-18s/%-6s manual padding: the rendered
// output must no longer contain a fixed 18-char label run, yet must still carry
// every key label and value.
func TestRenderCompleteUsesTable(t *testing.T) {
	m := NewModel(true)
	m.audioProfile = &AudioProfile{
		Duration:     185 * time.Second,
		PeakLevel:    -1.2,
		RMSLevel:     -14.8,
		DynamicRange: 13.6,
		OptimalScale: 1.234,
		AnalysisTime: 1500 * time.Millisecond,
	}
	m.complete = &RenderComplete{
		OutputFile:    "out.mp4",
		TotalFrames:   4500,
		ThumbnailTime: 50 * time.Millisecond,
		VisTime:       3 * time.Second,
		EncodeTime:    8 * time.Second,
		AudioTime:     900 * time.Millisecond,
		TotalTime:     13 * time.Second,
		FileSize:      10485760,
		EncoderName:   "libx264",
	}

	out := stripStyles(m.renderComplete())

	// The old writeRow/writeBarRow padded each label to a fixed 18-character
	// field via %-18s, so a label was always followed by spaces up to column 18
	// (e.g. "Duration:" + 9 trailing spaces). The table aligns columns to the
	// widest cell instead, so those padded-to-18 runs must be gone.
	fixed18 := map[string]string{
		"Duration:":      "Duration:" + strings.Repeat(" ", 18-len("Duration:")),
		"Peak Level:":    "Peak Level:" + strings.Repeat(" ", 18-len("Peak Level:")),
		"Total time:":    "Total time:" + strings.Repeat(" ", 18-len("Total time:")),
		"Visualisation:": "Visualisation:" + strings.Repeat(" ", 18-len("Visualisation:")),
	}
	for label, padded := range fixed18 {
		if strings.Contains(out, padded) {
			t.Errorf("output still contains the old %%-18s padding for %q (fixed 18-char label run)", label)
		}
	}

	// The same information must survive the layout swap: section headers, every
	// Pass 1 label and Pass 2 breakdown label, and representative values.
	wantSubstrings := []string{
		"Pass 1: Audio Analysis",
		"Pass 2: Rendering & Encoding",
		"Duration:", "185.0s",
		"Peak Level:", "-1.2 dB",
		"RMS Level:", "-14.8 dB",
		"Dynamic Range:", "13.6 dB",
		"Optimal Scale:", "1.234",
		"Analysis Time:", "1.5s",
		"Thumbnail:",
		"Visualisation:",
		"Video encoding:",
		"Audio encoding:",
		"Total time:", "13.0s",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("completion summary missing %q", want)
		}
	}

	// CompletionSummary() shares the render path used post-exit from main.go; it
	// must produce the same table-based summary.
	if !strings.Contains(stripStyles(m.CompletionSummary()), "Pass 1: Audio Analysis") {
		t.Error("CompletionSummary missing table-based Pass 1 section")
	}
}
