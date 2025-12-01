package ui

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Fire colour palette 🔥
var (
	// Core fire colours (dark to bright)
	fireYellow  = lipgloss.Color("#FFD700") // Bright yellow
	fireOrange  = lipgloss.Color("#FF8C00") // Deep orange
	fireRed     = lipgloss.Color("#FF4500") // Orange-red
	fireCrimson = lipgloss.Color("#DC143C") // Deep crimson
	emberGlow   = lipgloss.Color("#8B0000") // Dark ember red

	// Accent colours
	warmGray = lipgloss.Color("#B8860B") // Dark goldenrod for subtle text
)

// Phase represents the current processing phase
type Phase int

const (
	PhaseAnalysis Phase = iota
	PhaseRendering
	PhaseComplete
)

// AnalysisProgress represents progress updates from Pass 1 audio analysis
type AnalysisProgress struct {
	Frame       int
	TotalFrames int
	CurrentRMS  float64
	CurrentPeak float64
	BarHeights  []float64
	Duration    time.Duration
}

// AnalysisComplete signals completion of Pass 1 with audio profile data
type AnalysisComplete struct {
	PeakMagnitude float64
	RMSLevel      float64
	DynamicRange  float64
	Duration      time.Duration
	OptimalScale  float64
	AnalysisTime  time.Duration
}

// RenderProgress represents progress updates from Pass 2 video rendering
type RenderProgress struct {
	Frame       int
	TotalFrames int
	Elapsed     time.Duration
	BarHeights  []float64
	FileSize    int64
	Sensitivity float64
	FrameData   *image.RGBA
	VideoCodec  string
	AudioCodec  string
}

// AudioFlushProgress represents progress during audio flushing
type AudioFlushProgress struct {
	PacketsProcessed int
	Elapsed          time.Duration
}

// RenderComplete signals completion of Pass 2
type RenderComplete struct {
	OutputFile       string
	Duration         time.Duration
	FileSize         int64
	TotalFrames      int
	VisTime          time.Duration // Visualisation: FFT + binning + drawing
	EncodeTime       time.Duration // Video encoding time
	AudioTime        time.Duration // Audio reading + encoding time
	FinalizeTime     time.Duration // Encoder finalization (flush + close)
	TotalTime        time.Duration
	ThumbnailTime    time.Duration
	SamplesProcessed int64
	EncoderName      string // Video encoder used (e.g., "h264_nvenc", "libx264")
}

// AudioProfile holds the audio analysis results for display
type AudioProfile struct {
	Duration     time.Duration
	PeakLevel    float64 // in dB
	RMSLevel     float64 // in dB
	DynamicRange float64 // in dB
	OptimalScale float64
	AnalysisTime time.Duration
}

// progressQuitMsg is sent when it's time to quit after showing completion
type progressQuitMsg struct{}

// Model implements the unified Bubbletea model for both passes
type Model struct {
	progressBar progress.Model
	summaryBar  progress.Model
	phase       Phase

	// Audio profile (populated during/after Pass 1)
	audioProfile *AudioProfile

	// Pass 1 state
	analysisProgress AnalysisProgress

	// Pass 2 state
	renderState RenderProgress
	complete    *RenderComplete

	// Timing
	overallStartTime time.Time
	pass1StartTime   time.Time
	pass2StartTime   time.Time
	completionTime   time.Time

	// UI state
	width           int
	height          int
	noPreview       bool
	cachedPreview   string
	cachedFrameNum  int
	completionDelay time.Duration
	quitting        bool
}

// NewModel creates a new unified progress UI model
func NewModel(noPreview bool) *Model {
	// Fire gradient: deep red → orange → yellow
	p := progress.New(
		progress.WithGradient(string(fireCrimson), string(fireYellow)),
		progress.WithWidth(40),
		progress.WithoutPercentage(),
	)

	// Smaller progress bar for summary performance charts
	summaryBar := progress.New(
		progress.WithGradient(string(fireCrimson), string(fireYellow)),
		progress.WithWidth(30),
		progress.WithoutPercentage(),
	)

	return &Model{
		progressBar:      p,
		summaryBar:       summaryBar,
		phase:            PhaseAnalysis,
		overallStartTime: time.Now(),
		pass1StartTime:   time.Now(),
		completionDelay:  2 * time.Second,
		noPreview:        noPreview,
	}
}

// Init initializes the model
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.progressBar.Width = min(msg.Width-30, 50)
		return m, nil

	case AnalysisProgress:
		m.analysisProgress = msg
		return m, nil

	case AnalysisComplete:
		// Store audio profile for display
		m.audioProfile = &AudioProfile{
			Duration:     msg.Duration,
			PeakLevel:    20 * math.Log10(msg.PeakMagnitude),
			RMSLevel:     20 * math.Log10(msg.RMSLevel),
			DynamicRange: msg.DynamicRange,
			OptimalScale: msg.OptimalScale,
			AnalysisTime: msg.AnalysisTime,
		}
		// Transition to rendering phase
		m.phase = PhaseRendering
		m.pass2StartTime = time.Now()
		return m, nil

	case RenderProgress:
		m.renderState = msg
		return m, nil

	case RenderComplete:
		m.complete = &msg
		m.phase = PhaseComplete
		m.completionTime = time.Now()
		m.quitting = true

		return m, tea.Tick(m.completionDelay, func(t time.Time) tea.Msg {
			return progressQuitMsg{}
		})

	case progressQuitMsg:
		return m, tea.Quit

	case tea.KeyMsg:
		if m.complete != nil {
			return m, tea.Quit
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}

	return m, nil
}

// View renders the UI
func (m *Model) View() string {
	if m.phase == PhaseComplete {
		return m.renderFinalProgress() + "\n" + m.renderComplete()
	}
	return m.renderProgress()
}

// CompletionSummary returns the final completion summary for printing after alt screen exits.
// Returns empty string if encoding is not complete.
func (m *Model) CompletionSummary() string {
	if m.complete == nil {
		return ""
	}
	return m.renderFinalProgress() + "\n" + m.renderComplete()
}

// renderFinalProgress renders the progress UI in its final completed state
func (m *Model) renderFinalProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(fireYellow).
		Render("Jivefire 🔥")

	s.WriteString(title)
	s.WriteString("\n")
	s.WriteString(lipgloss.NewStyle().Foreground(fireOrange).Render("Pass 2: Rendering & Encoding"))
	s.WriteString("\n\n")

	// Progress bar at 100%
	progressBar := m.progressBar.ViewAs(1.0)
	s.WriteString("Progress: ")
	s.WriteString(progressBar)
	s.WriteString("  100%")
	s.WriteString("\n\n")

	// Final timing - calculate and display final speed
	videoDuration := time.Duration(m.complete.TotalFrames) * time.Second / 30
	var finalSpeed float64
	if m.complete.TotalTime > 0 {
		finalSpeed = float64(videoDuration) / float64(m.complete.TotalTime)
	}
	s.WriteString(lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("Time: %s  │  Speed: %.1fx realtime  │  Complete", formatDuration(m.complete.TotalTime), finalSpeed)))
	s.WriteString("\n")

	// Audio Profile
	s.WriteString("\n")
	m.renderAudioProfile(&s)

	// Final spectrum - zeroed out to show clean state
	spectrumWidth := m.width - 4
	if spectrumWidth < 10 {
		spectrumWidth = 100 // default if terminal size not set
	} else if spectrumWidth > 100 {
		spectrumWidth = 100
	}
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Live Visualisation:"))
	s.WriteString("\n")
	// Create zeroed bar heights for clean display
	zeroedBars := make([]float64, 64)
	spectrum := renderSpectrum(zeroedBars, spectrumWidth)
	s.WriteString(spectrum)

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(fireOrange).
		Padding(1, 2).
		Render(s.String())
}

func (m *Model) renderProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(fireYellow).
		Render("Jivefire 🔥")

	s.WriteString(title)
	s.WriteString("\n")

	// Phase indicator
	var phaseLabel string
	if m.phase == PhaseAnalysis {
		phaseLabel = "Pass 1: Analysing Audio"
	} else {
		phaseLabel = "Pass 2: Rendering & Encoding"
	}
	s.WriteString(lipgloss.NewStyle().Foreground(fireOrange).Render(phaseLabel))
	s.WriteString("\n\n")

	// Progress bar and timing
	if m.phase == PhaseAnalysis {
		m.renderAnalysisProgress(&s)
	} else {
		m.renderRenderingProgress(&s)
	}

	// Audio Profile (always shown, placeholder if not yet available)
	s.WriteString("\n")
	m.renderAudioProfile(&s)

	// Spectrum (Pass 2 only)
	if m.phase == PhaseRendering && len(m.renderState.BarHeights) > 0 {
		s.WriteString("\n")
		m.renderSpectrumAndStats(&s)
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(fireRed).
		Padding(1, 2).
		Render(s.String())
}

func (m *Model) renderAnalysisProgress(s *strings.Builder) {
	if m.analysisProgress.TotalFrames > 0 {
		// We have frame count, show progress bar
		percent := float64(m.analysisProgress.Frame) / float64(m.analysisProgress.TotalFrames)
		progressBar := m.progressBar.ViewAs(percent)

		s.WriteString("Progress: ")
		s.WriteString(progressBar)
		s.WriteString(fmt.Sprintf("  %d%%", int(percent*100)))
		s.WriteString("\n\n")
	} else if m.analysisProgress.Frame > 0 {
		// No total, show frame count with elapsed time
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Analysing..."))
		s.WriteString(fmt.Sprintf("  %d frames  │  Elapsed: %s\n\n",
			m.analysisProgress.Frame,
			formatDuration(m.analysisProgress.Duration)))
	} else {
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting analysis...\n\n"))
	}
}

func (m *Model) renderRenderingProgress(s *strings.Builder) {
	if m.renderState.TotalFrames == 0 {
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting render...\n\n"))
		return
	}

	// Progress is based on video frames (audio is encoded alongside each frame)
	percent := float64(m.renderState.Frame) / float64(m.renderState.TotalFrames)
	currentPhase := fmt.Sprintf("Frame %d of %d", m.renderState.Frame, m.renderState.TotalFrames)

	progressBar := m.progressBar.ViewAs(percent)
	s.WriteString("Progress: ")
	s.WriteString(progressBar)
	s.WriteString(fmt.Sprintf("  %d%%", int(percent*100)))
	s.WriteString("\n\n")

	// Timing information
	elapsed := m.renderState.Elapsed
	if elapsed == 0 {
		elapsed = time.Since(m.pass2StartTime)
	}

	var estimatedTotal, eta time.Duration
	var speed float64

	if percent > 0 {
		estimatedTotal = time.Duration(float64(elapsed) / percent)
		eta = estimatedTotal - elapsed

		videoEncodedSoFar := time.Duration(m.renderState.Frame) * time.Second / 30
		if elapsed > 0 {
			speed = float64(videoEncodedSoFar) / float64(elapsed)
		}
	}

	timingInfo := fmt.Sprintf("Time: %s / %s  │  Speed: %.1fx realtime  │  ETA: %s",
		formatDuration(elapsed),
		formatDuration(estimatedTotal),
		speed,
		formatDuration(eta))

	s.WriteString(lipgloss.NewStyle().Faint(true).Render(timingInfo))
	s.WriteString("\n")

	phaseStyle := lipgloss.NewStyle().Faint(true).Italic(true)
	s.WriteString(phaseStyle.Render(currentPhase))
}

func (m *Model) renderAudioProfile(s *strings.Builder) {
	labelStyle := lipgloss.NewStyle().Faint(true)
	valueStyle := lipgloss.NewStyle()
	headerStyle := lipgloss.NewStyle().Faint(true).Bold(true)

	s.WriteString(headerStyle.Render("Audio"))
	s.WriteString(" │ ")

	if m.audioProfile != nil {
		// Populated with real data
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.1fs", m.audioProfile.Duration.Seconds())))
		s.WriteString("  ")
		s.WriteString(labelStyle.Render("Peak:"))
		s.WriteString(" ")
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.PeakLevel)))
		s.WriteString("  ")
		s.WriteString(labelStyle.Render("RMS:"))
		s.WriteString(" ")
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.RMSLevel)))
		s.WriteString("  ")
		s.WriteString(labelStyle.Render("Range:"))
		s.WriteString(" ")
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.DynamicRange)))
		s.WriteString("  ")
		s.WriteString(labelStyle.Render("Scale:"))
		s.WriteString(" ")
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.3f", m.audioProfile.OptimalScale)))
	} else {
		// Placeholder during Pass 1
		placeholderStyle := lipgloss.NewStyle().Faint(true).Italic(true)
		s.WriteString(placeholderStyle.Render("Analysing..."))
	}
}

func (m *Model) renderSpectrumAndStats(s *strings.Builder) {
	s.WriteString(lipgloss.NewStyle().Foreground(fireOrange).Render("Live Visualisation:"))
	s.WriteString("\n")

	// Use full width for spectrum (64 bars matches the actual bar count)
	spectrumWidth := 64
	if m.width > 10 {
		spectrumWidth = min(m.width-10, 64)
	}
	spectrum := renderSpectrum(m.renderState.BarHeights, spectrumWidth)

	var rightCol strings.Builder
	if m.renderState.FileSize > 0 || m.renderState.VideoCodec != "" || m.renderState.AudioCodec != "" {
		labelStyle := lipgloss.NewStyle().Foreground(warmGray)
		valueStyle := lipgloss.NewStyle().Bold(true)

		rightCol.WriteString(labelStyle.Render("File:  "))
		rightCol.WriteString(valueStyle.Render(formatBytes(m.renderState.FileSize)))
		rightCol.WriteString("\n")

		if m.renderState.VideoCodec != "" {
			rightCol.WriteString(labelStyle.Render("Video: "))
			rightCol.WriteString(valueStyle.Render(m.renderState.VideoCodec))
			rightCol.WriteString("\n")
		}

		if m.renderState.AudioCodec != "" {
			rightCol.WriteString(labelStyle.Render("Audio: "))
			rightCol.WriteString(valueStyle.Render(m.renderState.AudioCodec))
		}
	}

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		spectrum,
		"  ",
		rightCol.String()))

	// Video preview
	if !m.noPreview {
		if m.renderState.FrameData != nil && m.renderState.Frame != m.cachedFrameNum {
			config := DefaultPreviewConfig()
			preview := DownsampleFrame(m.renderState.FrameData, config)
			m.cachedPreview = RenderPreview(preview)
			m.cachedFrameNum = m.renderState.Frame
		}

		if m.cachedPreview != "" {
			s.WriteString("\n")
			s.WriteString(m.cachedPreview)
		}
	}
}

func (m *Model) renderComplete() string {
	var s strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(fireYellow).
		Render("✓ Encoding Complete!")

	s.WriteString(title)
	s.WriteString("\n\n")

	// Styles for output summary
	dimLabel := lipgloss.NewStyle().Faint(true)

	// Output summary
	s.WriteString(fmt.Sprintf("%s%s\n", dimLabel.Render("Output:   "), m.complete.OutputFile))
	if m.complete.EncoderName != "" {
		s.WriteString(fmt.Sprintf("%s%s\n", dimLabel.Render("Encoder:  "), m.complete.EncoderName))
	}

	videoDuration := time.Duration(m.complete.TotalFrames) * time.Second / 30
	s.WriteString(fmt.Sprintf("%s%d frames, %.2f fps average\n",
		dimLabel.Render("Video:    "),
		m.complete.TotalFrames,
		float64(m.complete.TotalFrames)/videoDuration.Seconds()))
	if m.complete.SamplesProcessed > 0 {
		s.WriteString(fmt.Sprintf("%s%d samples processed\n", dimLabel.Render("Audio:    "), m.complete.SamplesProcessed))
	}
	s.WriteString(fmt.Sprintf("%s%.1fs video in %.1fs\n",
		dimLabel.Render("Duration: "),
		videoDuration.Seconds(),
		m.complete.TotalTime.Seconds()))
	s.WriteString(fmt.Sprintf("%s%s\n\n", dimLabel.Render("Size:     "), formatBytes(m.complete.FileSize)))

	// Audio Profile section
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(fireOrange)
	labelStyle := lipgloss.NewStyle().Faint(true)
	valueStyle := lipgloss.NewStyle()
	highlightValueStyle := lipgloss.NewStyle().Foreground(fireOrange)

	s.WriteString(headerStyle.Render("Pass 1: Audio Analysis"))
	s.WriteString("\n")

	if m.audioProfile != nil {
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "Duration:")), valueStyle.Render(fmt.Sprintf("%.1fs", m.audioProfile.Duration.Seconds()))))
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "Peak Level:")), valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.PeakLevel))))
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "RMS Level:")), valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.RMSLevel))))
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "Dynamic Range:")), valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.DynamicRange))))
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "Optimal Scale:")), valueStyle.Render(fmt.Sprintf("%.3f", m.audioProfile.OptimalScale))))
		s.WriteString(fmt.Sprintf("  %s%s\n", labelStyle.Render(fmt.Sprintf("%-18s", "Analysis Time:")), highlightValueStyle.Render(formatDuration(m.audioProfile.AnalysisTime))))
	}

	s.WriteString("\n")

	// Pass 2 Performance Breakdown
	s.WriteString(headerStyle.Render("Pass 2: Rendering & Encoding"))
	s.WriteString("\n")

	totalMs := m.complete.TotalTime.Milliseconds()
	if totalMs == 0 {
		totalMs = 1
	}

	if m.complete.ThumbnailTime > 0 {
		s.WriteString(fmt.Sprintf("  %s%s (~%2d%%)  %s\n",
			labelStyle.Render(fmt.Sprintf("%-18s", "Thumbnail:")),
			valueStyle.Render(fmt.Sprintf("~%-6s", formatDuration(m.complete.ThumbnailTime))),
			int(float64(m.complete.ThumbnailTime.Milliseconds())*100/float64(totalMs)),
			m.summaryBar.ViewAs(float64(m.complete.ThumbnailTime.Milliseconds())/float64(totalMs))))
	}

	s.WriteString(fmt.Sprintf("  %s%s (~%2d%%)  %s\n",
		labelStyle.Render(fmt.Sprintf("%-18s", "Visualisation:")),
		valueStyle.Render(fmt.Sprintf("~%-6s", formatDuration(m.complete.VisTime))),
		int(float64(m.complete.VisTime.Milliseconds())*100/float64(totalMs)),
		m.summaryBar.ViewAs(float64(m.complete.VisTime.Milliseconds())/float64(totalMs))))

	s.WriteString(fmt.Sprintf("  %s%s (~%2d%%)  %s\n",
		labelStyle.Render(fmt.Sprintf("%-18s", "Video encoding:")),
		valueStyle.Render(fmt.Sprintf("~%-6s", formatDuration(m.complete.EncodeTime))),
		int(float64(m.complete.EncodeTime.Milliseconds())*100/float64(totalMs)),
		m.summaryBar.ViewAs(float64(m.complete.EncodeTime.Milliseconds())/float64(totalMs))))

	if m.complete.AudioTime > 0 {
		s.WriteString(fmt.Sprintf("  %s%s (~%2d%%)  %s\n",
			labelStyle.Render(fmt.Sprintf("%-18s", "Audio encoding:")),
			valueStyle.Render(fmt.Sprintf("~%-6s", formatDuration(m.complete.AudioTime))),
			int(float64(m.complete.AudioTime.Milliseconds())*100/float64(totalMs)),
			m.summaryBar.ViewAs(float64(m.complete.AudioTime.Milliseconds())/float64(totalMs))))
	}

	// Calculate unaccounted time including finalisation (Pass 2 only)
	// Roll finalisation into runtime/pipeline since it's typically small
	accountedTime := m.complete.ThumbnailTime + m.complete.VisTime +
		m.complete.EncodeTime + m.complete.AudioTime
	otherTime := m.complete.TotalTime - accountedTime
	if otherTime > 0 {
		// Label based on encoder type: hardware encoders have GPU pipeline overhead,
		// software encoder has Go runtime/GC overhead
		otherLabel := "Runtime:"
		if strings.Contains(m.complete.EncoderName, "nvenc") ||
			strings.Contains(m.complete.EncoderName, "vulkan") ||
			strings.Contains(m.complete.EncoderName, "vaapi") ||
			strings.Contains(m.complete.EncoderName, "qsv") ||
			strings.Contains(m.complete.EncoderName, "videotoolbox") {
			otherLabel = "GPU pipeline:"
		}
		s.WriteString(fmt.Sprintf("  %s%s (~%2d%%)  %s\n",
			labelStyle.Render(fmt.Sprintf("%-18s", otherLabel)),
			valueStyle.Render(fmt.Sprintf("~%-6s", formatDuration(otherTime))),
			int(float64(otherTime.Milliseconds())*100/float64(totalMs)),
			m.summaryBar.ViewAs(float64(otherTime.Milliseconds())/float64(totalMs))))
	}

	s.WriteString(fmt.Sprintf("  %s%s", labelStyle.Render(fmt.Sprintf("%-18s", "Total time:")), highlightValueStyle.Render(formatDuration(m.complete.TotalTime))))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(fireOrange).
		Padding(1, 1).
		Render(s.String()) + "\n"
}

// Helper functions

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}

	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KB", "MB", "GB"}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

func makeSparkline(ratio float64, width int) string {
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}

	var result strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			// Fire gradient: dark red → red → orange → yellow based on position
			pos := float64(i) / float64(width)
			var color lipgloss.Color
			if pos < 0.25 {
				color = emberGlow
			} else if pos < 0.5 {
				color = fireCrimson
			} else if pos < 0.75 {
				color = fireOrange
			} else {
				color = fireYellow
			}
			styledBlock := lipgloss.NewStyle().Foreground(color).Render("█")
			result.WriteString(styledBlock)
		} else {
			// Empty block in warm gray
			styledBlock := lipgloss.NewStyle().Foreground(lipgloss.Color("#3A3A3A")).Render("░")
			result.WriteString(styledBlock)
		}
	}

	return result.String()
}

// makeGradientBar creates a subtle gradient progress bar similar to the main progress bar
func makeGradientBar(ratio float64, width int) string {
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	var result strings.Builder

	// Gradient colours from left to right (subtle fire gradient)
	gradientColors := []lipgloss.Color{
		lipgloss.Color("#8B0000"), // Dark red
		lipgloss.Color("#A52A2A"), // Brown-red
		lipgloss.Color("#CD5C5C"), // Indian red
		lipgloss.Color("#DC143C"), // Crimson
		lipgloss.Color("#FF6347"), // Tomato
		lipgloss.Color("#FF7F50"), // Coral
		lipgloss.Color("#FFA07A"), // Light salmon
		lipgloss.Color("#FFD700"), // Gold
	}

	for i := 0; i < width; i++ {
		if i < filled {
			// Interpolate colour based on position within the filled portion
			pos := float64(i) / float64(width)
			colorIdx := int(pos * float64(len(gradientColors)-1))
			if colorIdx >= len(gradientColors) {
				colorIdx = len(gradientColors) - 1
			}
			styledBlock := lipgloss.NewStyle().Foreground(gradientColors[colorIdx]).Render("█")
			result.WriteString(styledBlock)
		} else {
			// Empty portion - subtle dark background
			styledBlock := lipgloss.NewStyle().Foreground(lipgloss.Color("#2A2A2A")).Render("░")
			result.WriteString(styledBlock)
		}
	}

	return result.String()
}

// renderSpectrum creates a fire-coloured ASCII visualisation of bar heights
// Now renders 2 rows tall for better visibility
func renderSpectrum(barHeights []float64, width int) string {
	if len(barHeights) == 0 || width == 0 {
		return ""
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

	// Fire gradient colours from low to high intensity
	fireColors := []lipgloss.Color{
		lipgloss.Color("#8B0000"), // Dark red (ember)
		lipgloss.Color("#B22222"), // Firebrick
		lipgloss.Color("#DC143C"), // Crimson
		lipgloss.Color("#FF4500"), // Orange-red
		lipgloss.Color("#FF6347"), // Tomato
		lipgloss.Color("#FF8C00"), // Dark orange
		lipgloss.Color("#FFA500"), // Orange
		lipgloss.Color("#FFD700"), // Gold/Yellow
	}

	// Sample bars to fit width
	stride := len(barHeights) / width
	if stride == 0 {
		stride = 1
	}

	// Find max height for normalisation
	maxHeight := 0.0
	for _, h := range barHeights {
		if h > maxHeight {
			maxHeight = h
		}
	}

	if maxHeight == 0 {
		maxHeight = 1.0 // Avoid division by zero
	}

	// Collect normalised heights for all bars we'll display
	displayHeights := make([]float64, 0, width)
	for i := 0; i < len(barHeights) && len(displayHeights) < width; i += stride {
		displayHeights = append(displayHeights, barHeights[i]/maxHeight)
	}

	var result strings.Builder

	// Render top row (upper half of bars, only shows if height > 0.5)
	for _, normalised := range displayHeights {
		// Top row: shows the portion above 0.5
		if normalised > 0.5 {
			// Map 0.5-1.0 to block index 0-7
			topPortion := (normalised - 0.5) * 2.0 // 0.0 to 1.0
			blockIdx := int(topPortion * float64(len(blocks)-1))
			if blockIdx >= len(blocks) {
				blockIdx = len(blocks) - 1
			}

			// Colour based on overall height (hotter = higher)
			colorIdx := int(normalised * float64(len(fireColors)-1))
			if colorIdx >= len(fireColors) {
				colorIdx = len(fireColors) - 1
			}

			styledBlock := lipgloss.NewStyle().
				Foreground(fireColors[colorIdx]).
				Render(string(blocks[blockIdx]))
			result.WriteString(styledBlock)
		} else {
			// Empty space for bars that don't reach this row
			result.WriteString(" ")
		}
	}

	result.WriteString("\n")

	// Render bottom row (lower half of bars)
	for _, normalised := range displayHeights {
		var blockIdx int
		if normalised >= 0.5 {
			// Full block for bottom row if bar extends to top row
			blockIdx = len(blocks) - 1
		} else {
			// Map 0.0-0.5 to block index 0-7
			blockIdx = int(normalised * 2.0 * float64(len(blocks)-1))
			if blockIdx >= len(blocks) {
				blockIdx = len(blocks) - 1
			}
		}

		// Colour based on overall height
		colorIdx := int(normalised * float64(len(fireColors)-1))
		if colorIdx >= len(fireColors) {
			colorIdx = len(fireColors) - 1
		}
		if colorIdx < 0 {
			colorIdx = 0
		}

		styledBlock := lipgloss.NewStyle().
			Foreground(fireColors[colorIdx]).
			Render(string(blocks[blockIdx]))
		result.WriteString(styledBlock)
	}

	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
