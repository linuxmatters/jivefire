package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Pass1Progress represents progress updates from Pass 1 audio analysis
type Pass1Progress struct {
	Frame       int
	TotalFrames int
	CurrentRMS  float64
	CurrentPeak float64
	BarHeights  []float64
	Duration    time.Duration
}

// Pass1Complete signals completion of Pass 1
type Pass1Complete struct {
	PeakMagnitude float64
	RMSLevel      float64
	DynamicRange  float64
	Duration      time.Duration
	OptimalScale  float64
}

// quitTimerMsg is sent when it's time to quit after showing completion
type quitTimerMsg struct{}

// pass1Model implements the Bubbletea model for Pass 1
type pass1Model struct {
	progress        progress.Model
	lastUpdate      Pass1Progress
	complete        *Pass1Complete
	startTime       time.Time
	completionTime  time.Time
	width           int
	height          int
	minDisplayTime  time.Duration // Minimum time to show UI
	completionDelay time.Duration // Time to show completion screen
	quitting        bool          // Flag to indicate we're in quit delay
}

// NewPass1Model creates a new Pass 1 UI model
func NewPass1Model() tea.Model {
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(60),
	)

	return &pass1Model{
		progress:        p,
		startTime:       time.Now(),
		minDisplayTime:  500 * time.Millisecond,
		completionDelay: 250 * time.Millisecond,
		quitting:        false,
	}
}

// Init initializes the model
func (m *pass1Model) Init() tea.Cmd {
	return nil
}

// Update handles messages
func (m *pass1Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.progress.Width = min(msg.Width-20, 80)
		return m, nil

	case Pass1Progress:
		m.lastUpdate = msg
		return m, nil

	case Pass1Complete:
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
			return quitTimerMsg{}
		})

	case quitTimerMsg:
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
func (m *pass1Model) View() string {
	if m.complete != nil {
		return m.renderComplete()
	}

	return m.renderProgress()
}

func (m *pass1Model) renderProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#A40000")).
		Render("Jivefire 🔥")

	s.WriteString(title)
	s.WriteString("\n")
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Pass 1: Analyzing Audio"))
	s.WriteString("\n\n")

	// Progress bar
	if m.lastUpdate.TotalFrames > 0 {
		percent := float64(m.lastUpdate.Frame) / float64(m.lastUpdate.TotalFrames)
		s.WriteString(m.progress.ViewAs(percent))
		s.WriteString(fmt.Sprintf(" %d%% (%d/%d)\n\n",
			int(percent*100),
			m.lastUpdate.Frame,
			m.lastUpdate.TotalFrames))
	} else {
		s.WriteString(m.progress.ViewAs(0))
		s.WriteString(" 0% (0/0)\n\n")
	}

	// Live Spectrum Preview
	if len(m.lastUpdate.BarHeights) > 0 {
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Live Spectrum Preview:"))
		s.WriteString("\n")
		spectrum := renderSpectrum(m.lastUpdate.BarHeights, min(m.width-4, 76))
		s.WriteString(spectrum)
		s.WriteString("\n\n")
	}

	// Audio Stats
	if m.lastUpdate.Frame > 0 {
		statsStyle := lipgloss.NewStyle().Faint(true)

		duration := m.lastUpdate.Duration.Seconds()
		sampleRate := 44.1 // kHz - could be made dynamic

		s.WriteString(statsStyle.Render("Audio Stats:"))
		s.WriteString("\n")

		leftCol := fmt.Sprintf("  Duration:       %.1fs", duration)
		rightCol := fmt.Sprintf("Sample Rate:  %.1f kHz", sampleRate)
		s.WriteString(leftCol + "  │  " + rightCol + "\n")

		leftCol = fmt.Sprintf("  Peak Level:     %.1f dB", 20*math.Log10(m.lastUpdate.CurrentPeak))
		rightCol = fmt.Sprintf("RMS Level:    %.1f dB", 20*math.Log10(m.lastUpdate.CurrentRMS))
		s.WriteString(leftCol + "  │  " + rightCol + "\n")

		s.WriteString("\n")
	}

	// Estimated time remaining
	if m.lastUpdate.Frame > 0 && m.lastUpdate.TotalFrames > 0 {
		elapsed := time.Since(m.startTime)
		framesPerSec := float64(m.lastUpdate.Frame) / elapsed.Seconds()
		remaining := float64(m.lastUpdate.TotalFrames-m.lastUpdate.Frame) / framesPerSec

		s.WriteString(lipgloss.NewStyle().Faint(true).Render(
			fmt.Sprintf("Estimated Time Remaining: %.1fs", remaining)))
		s.WriteString("\n")
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#A40000")).
		Padding(1, 2).
		Render(s.String())
}

func (m *pass1Model) renderComplete() string {
	var s strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#4A9B4A")).
		Render("✓ Analysis Complete!")

	s.WriteString(title)
	s.WriteString("\n\n")

	// Audio Profile Summary
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Audio Profile:"))
	s.WriteString("\n")

	elapsed := m.complete.Duration

	s.WriteString(fmt.Sprintf("  Duration:         %.1fs\n", elapsed.Seconds()))
	s.WriteString(fmt.Sprintf("  Peak Level:       %.1f dB\n",
		20*math.Log10(m.complete.PeakMagnitude)))
	s.WriteString(fmt.Sprintf("  RMS Level:        %.1f dB\n",
		20*math.Log10(m.complete.RMSLevel)))
	s.WriteString(fmt.Sprintf("  Dynamic Range:    %.1f dB\n",
		m.complete.DynamicRange))
	s.WriteString(fmt.Sprintf("  Optimal Scale:    %.3f\n\n",
		m.complete.OptimalScale))

	processingTime := time.Since(m.startTime)
	s.WriteString(fmt.Sprintf("Analysis completed in %.2fs", processingTime.Seconds()))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#4A9B4A")).
		Padding(1, 2).
		Render(s.String()) + "\n"
}

// renderSpectrum creates an ASCII visualization of bar heights
func renderSpectrum(barHeights []float64, width int) string {
	if len(barHeights) == 0 || width == 0 {
		return ""
	}

	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

	// Sample bars to fit width
	stride := len(barHeights) / width
	if stride == 0 {
		stride = 1
	}

	// Find max height for normalization
	maxHeight := 0.0
	for _, h := range barHeights {
		if h > maxHeight {
			maxHeight = h
		}
	}

	if maxHeight == 0 {
		maxHeight = 1.0 // Avoid division by zero
	}

	var result strings.Builder
	for i := 0; i < len(barHeights); i += stride {
		if result.Len() >= width {
			break
		}

		height := barHeights[i]
		normalized := height / maxHeight // 0.0 to 1.0
		blockIdx := int(normalized * float64(len(blocks)-1))
		if blockIdx >= len(blocks) {
			blockIdx = len(blocks) - 1
		}

		result.WriteRune(blocks[blockIdx])
	}

	return result.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
