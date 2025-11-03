package config

// Video settings
const (
	Width  = 1280
	Height = 720
	FPS    = 30
)

// Audio settings
const (
	SampleRate = 44100
	FFTSize    = 2048
)

// Visualization settings
const (
	NumBars      = 64   // Number of bars
	BarWidth     = 12   // Width of each bar
	BarGap       = 8    // Gap between bars
	CenterGap    = 100  // Gap between top and bottom bar sections
	MaxBarHeight = 0.50 // Maximum bar height as fraction of available space
)

// Colors
const (
	BarColorR = 164
	BarColorG = 0
	BarColorB = 0
)

// CAVA algorithm constants
const (
	Framerate       = 30.0
	NoiseReduction  = 0.77  // CAVA default integral smoothing
	FallAccel       = 0.028 // CAVA gravity acceleration constant
)
