package ui

import (
	"fmt"
	"image"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Pass2Progress represents progress updates from Pass 2 video rendering
type Pass2Progress struct {
	Frame       int
	TotalFrames int
	Elapsed     time.Duration
	BarHeights  []float64
	FileSize    int64       // Estimated current file size in bytes
	Sensitivity float64     // Current sensitivity value
	FrameData   *image.RGBA // Current frame RGB data for video preview (optional)
	VideoCodec  string      // Video codec info (e.g., "H.264 1920×1080")
	AudioCodec  string      // Audio codec info (e.g., "AAC 48kHz stereo")
}

// Pass2AudioFlush represents progress during audio flushing
type Pass2AudioFlush struct {
	PacketsProcessed int
	Elapsed          time.Duration
}

// Pass2Complete signals completion of Pass 2
type Pass2Complete struct {
	OutputFile       string
	Duration         time.Duration
	FileSize         int64
	TotalFrames      int
	FFTTime          time.Duration
	BinTime          time.Duration
	DrawTime         time.Duration
	EncodeTime       time.Duration
	AudioFlushTime   time.Duration
	TotalTime        time.Duration
	Pass1Time        time.Duration // Time spent in Pass 1 analysis
	SamplesProcessed int64
}

// quitTimerMsg2 is sent when it's time to quit after showing completion
type quitTimerMsg2 struct{}

// pass2Model implements the Bubbletea model for Pass 2
type pass2Model struct {
	progress        progress.Model
	lastUpdate      Pass2Progress
	audioFlush      *Pass2AudioFlush // Audio flush progress
	complete        *Pass2Complete
	startTime       time.Time
	completionTime  time.Time
	width           int
	height          int
	minDisplayTime  time.Duration // Minimum time to show UI
	completionDelay time.Duration // Time to show completion screen
	quitting        bool          // Flag to indicate we're in quit delay
	cachedPreview   string        // Cached rendered preview string
	cachedFrameNum  int           // Frame number of cached preview
	noPreview       bool          // Disable video preview for better batch performance

	// For unified progress tracking
	estimatedAudioPackets int  // Estimated audio packets to process
	inAudioPhase          bool // Whether we're in audio flush phase
}

// NewPass2Model creates a new Pass 2 UI model
func NewPass2Model(noPreview bool) tea.Model {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(40),
		progress.WithoutPercentage(), // Hide built-in percentage display
	)

	return &pass2Model{
		progress:        p,
		startTime:       time.Now(),
		minDisplayTime:  500 * time.Millisecond, // Show UI for at least 0.5 seconds
		completionDelay: 2 * time.Second,        // Show completion for 2 seconds
		quitting:        false,
		noPreview:       noPreview,
	}
}

// Init initializes the model
func (m *pass2Model) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *pass2Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.progress.Width = min(msg.Width-30, 50)
		return m, nil

	case Pass2Progress:
		m.lastUpdate = msg
		m.audioFlush = nil // Clear audio flush when video is progressing
		m.inAudioPhase = false
		// Estimate audio packets based on video frames (roughly 0.36 packets per frame)
		m.estimatedAudioPackets = int(float64(msg.TotalFrames) * 0.36)
		return m, nil

	case Pass2AudioFlush:
		m.audioFlush = &msg
		if !m.inAudioPhase {
			m.inAudioPhase = true
			// Refine estimate if actual count is higher
			if msg.PacketsProcessed > m.estimatedAudioPackets {
				m.estimatedAudioPackets = int(float64(msg.PacketsProcessed) * 1.1) // Add 10% buffer
			}
		}
		return m, nil

	case Pass2Complete:
		m.complete = &msg
		m.completionTime = time.Now()
		m.quitting = true

		// Calculate how long to show completion screen
		elapsed := m.completionTime.Sub(m.startTime)
		delay := m.completionDelay

		// If total time is less than minDisplayTime, extend completion delay
		if elapsed < m.minDisplayTime {
			additionalTime := m.minDisplayTime - elapsed
			delay = m.completionDelay + additionalTime
		}

		// Show completion screen for calculated delay before quitting
		return m, tea.Tick(delay, func(t time.Time) tea.Msg {
			return quitTimerMsg2{}
		})

	case quitTimerMsg2:
		// Timer expired, now we can quit
		return m, tea.Quit

	case tea.KeyMsg:
		// Allow any key to skip the completion screen delay
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
func (m *pass2Model) View() string {
	if m.complete != nil {
		return m.renderComplete()
	}

	return m.renderProgress()
}

func (m *pass2Model) renderProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#A40000")).
		Render("Jivefire 🔥")

	subtitle := lipgloss.NewStyle().
		Faint(true).
		Render("Pass 2: Rendering & Encoding")

	s.WriteString(title)
	s.WriteString("\n")
	s.WriteString(subtitle)
	s.WriteString("\n\n")

	// Calculate unified progress across video and audio
	var overallPercent float64
	var statusText string
	var currentPhase string

	if m.lastUpdate.TotalFrames > 0 {
		// Total work = video frames (88%) + audio packets (12%)
		// Based on ~53s video + ~7s audio for a typical encode
		videoWeight := 0.88
		audioWeight := 0.12

		// Calculate video progress
		videoPercent := float64(m.lastUpdate.Frame) / float64(m.lastUpdate.TotalFrames)

		// Calculate audio progress if in audio phase
		audioPercent := 0.0
		if m.inAudioPhase && m.audioFlush != nil && m.estimatedAudioPackets > 0 {
			audioPercent = float64(m.audioFlush.PacketsProcessed) / float64(m.estimatedAudioPackets)
			if audioPercent > 1.0 {
				audioPercent = 1.0
			}
			currentPhase = "Finalizing audio streams"
		} else {
			currentPhase = fmt.Sprintf("Frame %d of %d", m.lastUpdate.Frame, m.lastUpdate.TotalFrames)
		}

		// Calculate overall progress
		if m.inAudioPhase {
			// Video is complete (90%) + audio progress (0-10%)
			overallPercent = videoWeight + (audioPercent * audioWeight)
			statusText = fmt.Sprintf("%d%%",
				int(overallPercent*100))
		} else {
			// Just video progress (0-90%)
			overallPercent = videoPercent * videoWeight
			statusText = fmt.Sprintf("%d%%",
				int(overallPercent*100))
		}

		// Render progress bar
		progressBar := m.progress.ViewAs(overallPercent)

		s.WriteString("Progress: ")
		s.WriteString(progressBar)
		s.WriteString("  ")
		s.WriteString(statusText)
		s.WriteString("\n\n")

		// Timing information
		elapsed := m.lastUpdate.Elapsed
		if elapsed == 0 {
			elapsed = time.Since(m.startTime)
		}

		// If in audio phase, use the audio flush elapsed time
		if m.inAudioPhase && m.audioFlush != nil && m.audioFlush.Elapsed > 0 {
			elapsed = m.audioFlush.Elapsed
		}

		// Calculate estimated total time and ETA
		var estimatedTotal time.Duration
		var eta time.Duration
		var speed float64

		if overallPercent > 0 {
			estimatedTotal = time.Duration(float64(elapsed) / overallPercent)
			eta = estimatedTotal - elapsed

			// Calculate speed as ratio of video duration to encoding time
			// Assuming 30 fps
			videoDuration := time.Duration(m.lastUpdate.TotalFrames) * time.Second / 30
			if elapsed > 0 {
				speed = float64(videoDuration) / float64(elapsed)
			}
		}

		timingInfo := fmt.Sprintf("Time: %s / %s  │  Speed: %.1fx realtime  │  ETA: %s",
			formatDuration(elapsed),
			formatDuration(estimatedTotal),
			speed,
			formatDuration(eta))

		s.WriteString(lipgloss.NewStyle().Faint(true).Render(timingInfo))
		s.WriteString("\n")

		// Show current phase
		phaseStyle := lipgloss.NewStyle().Faint(true).Italic(true)
		s.WriteString(phaseStyle.Render(currentPhase))
		s.WriteString("\n\n")
	}

	// Live Visualization and Stats side-by-side
	if len(m.lastUpdate.BarHeights) > 0 {
		// Live Visualization header
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Live Visualization:"))
		s.WriteString("\n")

		// Render spectrum bars and stats side-by-side
		var leftCol strings.Builder
		spectrum := renderSpectrum(m.lastUpdate.BarHeights, min(m.width-4, 72))
		leftCol.WriteString(spectrum)

		var rightCol strings.Builder
		if m.lastUpdate.FileSize > 0 || m.lastUpdate.VideoCodec != "" || m.lastUpdate.AudioCodec != "" {
			// Create styled stats display
			labelStyle := lipgloss.NewStyle().Faint(true)
			valueStyle := lipgloss.NewStyle().Bold(true)

			rightCol.WriteString(labelStyle.Render("File:  "))
			rightCol.WriteString(valueStyle.Render(formatBytes(m.lastUpdate.FileSize)))
			rightCol.WriteString("\n")

			if m.lastUpdate.VideoCodec != "" {
				rightCol.WriteString(labelStyle.Render("Video: "))
				rightCol.WriteString(valueStyle.Render(m.lastUpdate.VideoCodec))
				rightCol.WriteString("\n")
			}

			if m.lastUpdate.AudioCodec != "" {
				rightCol.WriteString(labelStyle.Render("Audio: "))
				rightCol.WriteString(valueStyle.Render(m.lastUpdate.AudioCodec))
			}
		}

		// Join horizontally with proper spacing
		s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			leftCol.String(),
			"  ", // spacing
			rightCol.String()))
		s.WriteString("\n")

		// Video preview - only render if not disabled
		if !m.noPreview {
			// Video preview - regenerate if new frame data available, otherwise use cached
			if m.lastUpdate.FrameData != nil && m.lastUpdate.Frame != m.cachedFrameNum {
				// New frame data available, regenerate preview
				config := DefaultPreviewConfig()
				preview := DownsampleFrame(m.lastUpdate.FrameData, config)
				m.cachedPreview = RenderPreview(preview)
				m.cachedFrameNum = m.lastUpdate.Frame
			}

			// Always display cached preview once we have one (prevents flickering)
			if m.cachedPreview != "" {
				s.WriteString("\n")
				s.WriteString(m.cachedPreview)
			}
		}
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#A40000")).
		Padding(1, 2).
		Render(s.String())
}

func (m *pass2Model) renderComplete() string {
	var s strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#4A9B4A")).
		Render("✓ Encoding Complete!")

	s.WriteString(title)
	s.WriteString("\n\n")

	// Output summary
	s.WriteString(fmt.Sprintf("Output:   %s\n", m.complete.OutputFile))

	// Calculate speed using total time (not just Pass 2)
	videoDuration := time.Duration(m.complete.TotalFrames) * time.Second / 30
	speed := float64(videoDuration) / float64(m.complete.TotalTime)

	s.WriteString(fmt.Sprintf("Duration: %.1fs video in %.1fs (%.1fx realtime)\n",
		videoDuration.Seconds(),
		m.complete.TotalTime.Seconds(),
		speed))

	s.WriteString(fmt.Sprintf("Size:     %s\n\n", formatBytes(m.complete.FileSize)))

	// Performance Breakdown
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Performance Breakdown:"))
	s.WriteString("\n")

	totalMs := m.complete.TotalTime.Milliseconds()
	if totalMs == 0 {
		totalMs = 1 // Avoid division by zero
	}

	// Pass 1 Analysis
	if m.complete.Pass1Time > 0 {
		s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
			"Analysis:",
			formatDuration(m.complete.Pass1Time),
			int(float64(m.complete.Pass1Time.Milliseconds())*100/float64(totalMs)),
			makeSparkline(float64(m.complete.Pass1Time.Milliseconds())/float64(totalMs), 30)))
	}

	// Pass 2 rendering components
	s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
		"FFT computation:",
		formatDuration(m.complete.FFTTime),
		int(float64(m.complete.FFTTime.Milliseconds())*100/float64(totalMs)),
		makeSparkline(float64(m.complete.FFTTime.Milliseconds())/float64(totalMs), 30)))

	s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
		"Bar binning:",
		formatDuration(m.complete.BinTime),
		int(float64(m.complete.BinTime.Milliseconds())*100/float64(totalMs)),
		makeSparkline(float64(m.complete.BinTime.Milliseconds())/float64(totalMs), 30)))

	s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
		"Rendering:",
		formatDuration(m.complete.DrawTime),
		int(float64(m.complete.DrawTime.Milliseconds())*100/float64(totalMs)),
		makeSparkline(float64(m.complete.DrawTime.Milliseconds())/float64(totalMs), 30)))

	s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
		"Video encoding:",
		formatDuration(m.complete.EncodeTime),
		int(float64(m.complete.EncodeTime.Milliseconds())*100/float64(totalMs)),
		makeSparkline(float64(m.complete.EncodeTime.Milliseconds())/float64(totalMs), 30)))

	// Audio finalization
	if m.complete.AudioFlushTime > 0 {
		s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
			"Audio finalization:",
			formatDuration(m.complete.AudioFlushTime),
			int(float64(m.complete.AudioFlushTime.Milliseconds())*100/float64(totalMs)),
			makeSparkline(float64(m.complete.AudioFlushTime.Milliseconds())/float64(totalMs), 30)))
	}

	// Calculate and display "other" time (overhead, I/O, etc.)
	accountedTime := m.complete.Pass1Time + m.complete.FFTTime + m.complete.BinTime +
		m.complete.DrawTime + m.complete.EncodeTime + m.complete.AudioFlushTime
	otherTime := m.complete.TotalTime - accountedTime
	if otherTime > 0 {
		s.WriteString(fmt.Sprintf("  %-20s%-5s  (%2d%%)  %s\n",
			"Initialization:",
			formatDuration(otherTime),
			int(float64(otherTime.Milliseconds())*100/float64(totalMs)),
			makeSparkline(float64(otherTime.Milliseconds())/float64(totalMs), 30)))
	}

	s.WriteString(fmt.Sprintf("  %-20s%s\n\n", "Total time:", formatDuration(m.complete.TotalTime)))

	// Quality Metrics
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Quality Metrics:"))
	s.WriteString("\n")
	s.WriteString(fmt.Sprintf("  Video: %d frames, %.2f fps average\n",
		m.complete.TotalFrames,
		float64(m.complete.TotalFrames)/videoDuration.Seconds()))

	if m.complete.SamplesProcessed > 0 {
		s.WriteString(fmt.Sprintf("  Audio: %d samples processed",
			m.complete.SamplesProcessed))
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#4A9B4A")).
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
	blocks := []rune{'░', '▓', '█'}

	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}

	var result strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			result.WriteRune(blocks[2]) // Full block
		} else {
			result.WriteRune(blocks[0]) // Empty block
		}
	}

	return result.String()
}
