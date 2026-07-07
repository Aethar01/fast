package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	// connections is the number of parallel downloads we use to saturate the
	// connection, the same as fast.com.
	connections = 5

	// duration is how long we measure the connection speed for.
	duration = 10 * time.Second

	// sparkWidth is the width, in cells, of the speed sparkline.
	sparkWidth = 20

	// latencyTimeout bounds the latency probe so a stalled request can't hang
	// the test indefinitely.
	latencyTimeout = 5 * time.Second
)

const (
	downloadColor = "#2EF8BB"
	uploadColor   = "#BD52FF"
)

const (
	downloadLabel = "↓"
	uploadLabel   = "↑"
)

var (
	speedStyle   = lipgloss.NewStyle().Bold(true)
	labelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	unitStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dlSparkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(downloadColor))
	ulSparkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(uploadColor))
	peakStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	baseStyle    = lipgloss.NewStyle().Padding(1, 2)
)

const tickInterval = time.Second / 10

type tickMsg time.Time

func tickCmd(t time.Time) tea.Msg {
	return tickMsg(t)
}

type Model struct {
	targets []string

	ping     time.Duration
	pingDone bool

	dlBytes  *atomic.Int64
	dlCtx    context.Context
	dlCancel context.CancelFunc
	dlStart  time.Time
	dlSpeed  float64
	dlSpeeds []float64
	dlPeak   float64
	dlDone   bool

	ulBytes  *atomic.Int64
	ulCtx    context.Context
	ulCancel context.CancelFunc
	ulStart  time.Time
	ulSpeed  float64
	ulSpeeds []float64
	ulPeak   float64
	ulDone   bool

	quitting bool
}

func NewModel(targets []string) Model {
	return Model{
		targets: targets,
		dlBytes: &atomic.Int64{},
		ulBytes: &atomic.Int64{},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(tickInterval, tickCmd)
}

// measureDownload kicks off the parallel downloads that feed our byte counter
func (m Model) measureDownload() tea.Msg {
	for _, url := range m.targets {
		go download(m.dlCtx, url, m.dlBytes)
	}
	return nil
}

// measureUpload kicks off the parallel uploads that feed our upload byte counter
func (m Model) measureUpload() tea.Msg {
	for _, url := range m.targets {
		go upload(m.ulCtx, url, m.ulBytes)
	}
	return nil
}

// pingMsg carries the result of the background latency probe.
type pingMsg struct {
	d   time.Duration
	err error
}

// measurePing probes the first target's round-trip time once the download and
// upload have finished, and the result is revealed in the summary.
func (m Model) measurePing() tea.Msg {
	if len(m.targets) == 0 {
		return pingMsg{err: errors.New("no targets")}
	}
	ctx, cancel := context.WithTimeout(context.Background(), latencyTimeout)
	defer cancel()
	d, err := latency(ctx, m.targets[0])
	return pingMsg{d: d, err: err}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			if m.dlCancel != nil {
				m.dlCancel()
			}
			if m.ulCancel != nil {
				m.ulCancel()
			}
			return m, tea.Quit
		}

	case pingMsg:
		m.pingDone = true
		if msg.err == nil {
			m.ping = msg.d
		}
		return m, tea.Quit

	case tickMsg:
		if m.dlCtx == nil {
			// First tick: kick off the download that feeds the byte counter.
			m.dlStart = time.Now()
			m.dlCtx, m.dlCancel = context.WithTimeout(context.Background(), duration)
			return m, tea.Batch(tea.Tick(tickInterval, tickCmd), m.measureDownload)
		} else if !m.dlDone {
			elapsed := time.Since(m.dlStart)
			m.dlSpeed = mbps(m.dlBytes.Load(), elapsed)
			m.dlSpeeds = append(m.dlSpeeds, m.dlSpeed)
			if m.dlSpeed > m.dlPeak {
				m.dlPeak = m.dlSpeed
			}

			if elapsed >= duration {
				m.dlDone = true
				if m.dlCancel != nil {
					m.dlCancel()
				}
				m.ulStart = time.Now()
				m.ulCtx, m.ulCancel = context.WithTimeout(context.Background(), duration)
				return m, tea.Batch(tea.Tick(tickInterval, tickCmd), m.measureUpload)
			}
		} else if !m.ulDone {
			elapsed := time.Since(m.ulStart)
			m.ulSpeed = mbps(m.ulBytes.Load(), elapsed)
			m.ulSpeeds = append(m.ulSpeeds, m.ulSpeed)
			if m.ulSpeed > m.ulPeak {
				m.ulPeak = m.ulSpeed
			}

			if elapsed >= duration {
				m.ulDone = true
				if m.ulCancel != nil {
					m.ulCancel()
				}
				// Transfers are done; measure ping last and reveal it in the
				// summary.
				return m, m.measurePing
			}
		}

		return m, tea.Tick(tickInterval, tickCmd)
	}

	return m, nil
}

func renderRow(label string, currentSpeed float64, speeds []float64, peak float64, sparkStyle lipgloss.Style) string {
	var s strings.Builder
	s.WriteString(sparkStyle.Render(label))
	s.WriteString(" ")
	// Cap each readout at 999.9 and switch to Gbps beyond that, keeping a fixed
	// width so the unit, sparkline, and peak never shift horizontally.
	speed, unit := scale(currentSpeed)
	s.WriteString(speedStyle.Render(fmt.Sprintf("%5.1f", speed)))
	s.WriteString(unitStyle.Render(" " + unit))
	s.WriteString(" ")
	s.WriteString(sparkStyle.Render(sparkline(speeds, peak, sparkWidth)))
	if peak > 0 {
		peakVal, peakUnit := scale(peak)
		// Only label the peak's unit when it differs from the live reading's.
		label := fmt.Sprintf("  peak %.0f", peakVal)
		if peakUnit != unit {
			label += " " + peakUnit
		}
		s.WriteString(peakStyle.Render(label))
	}

	return s.String()
}

// renderSummary renders the final one-line recap: download, upload and ping.
func renderSummary(m Model) string {
	sep := unitStyle.Render(" • ")
	var ping string
	if m.ping > 0 {
		ping = speedStyle.Render(fmt.Sprintf("%d", m.ping.Milliseconds())) + unitStyle.Render(" ms")
	} else {
		ping = unitStyle.Render("—")
	}
	return summarySpeed(downloadLabel, m.dlSpeed, dlSparkStyle) + sep +
		summarySpeed(uploadLabel, m.ulSpeed, ulSparkStyle) + sep + ping
}

// summarySpeed renders an accent-coloured arrow and its final speed.
func summarySpeed(label string, speed float64, sparkStyle lipgloss.Style) string {
	val, unit := scale(speed)
	return sparkStyle.Render(label) + " " +
		speedStyle.Render(fmt.Sprintf("%.1f", val)) +
		unitStyle.Render(" "+unit)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder
	switch {
	case !m.dlDone:
		// Downloading: show only the download reading.
		s.WriteString(renderRow(downloadLabel, m.dlSpeed, m.dlSpeeds, m.dlPeak, dlSparkStyle))
	case !m.pingDone:
		// Uploading, then holding the final upload reading while we measure
		// ping after the transfers finish.
		s.WriteString(renderRow(uploadLabel, m.ulSpeed, m.ulSpeeds, m.ulPeak, ulSparkStyle))
	default:
		// Everything done: one-line recap of download, upload and ping.
		s.WriteString(renderSummary(m))
	}

	style := baseStyle
	if m.pingDone {
		style = style.PaddingBottom(2)
	}
	return style.Render(s.String())
}

// mbps converts a number of bytes downloaded over a duration into megabits per
// second, the unit fast.com reports.
func mbps(bytes int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(bytes) * 8 / d.Seconds() / 1e6
}

// scale converts a speed in Mbps to its display magnitude and unit, switching to
// Gbps once it would read past 999.9 Mbps so the value never exceeds "999.9".
func scale(speed float64) (float64, string) {
	if speed >= 999.95 {
		return speed / 1000, "Gbps"
	}
	return speed, "Mbps"
}

func main() {
	urls, err := targets(connections)
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			fmt.Fprintln(os.Stderr, "No internet connection.")
			os.Exit(1)
		}
		log.Fatal(err)
	}

	if _, err := tea.NewProgram(NewModel(urls)).Run(); err != nil {
		log.Fatal(err)
	}
}
