package main

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/timer"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

var errNoTTY = errors.New("monitor: stdout is not a TTY")

const maxRoots = 25

type keyMap struct {
	Quit   key.Binding
	Up     key.Binding
	Down   key.Binding
	Top    key.Binding
	Bottom key.Binding
	Pause  key.Binding
	Clear  key.Binding
	Help   key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Top, k.Bottom, k.Pause, k.Clear, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down}, {k.Top, k.Bottom}, {k.Pause, k.Clear}, {k.Help, k.Quit}}
}

var keys = keyMap{
	Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Up:     key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/↓", "scroll")),
	Down:   key.NewBinding(key.WithKeys("down", "j")),
	Top:    key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
	Bottom: key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
	Pause:  key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pause")),
	Clear:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clear")),
	Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
}

type eventMsg sse.RecordMessage
type stateMsg sse.ConnectionState
type repaintMsg struct{}

type model struct {
	vp               viewport.Model
	help             help.Model
	spinner          spinner.Model
	retryTimer       timer.Model
	store            *state.Store
	staleOpenSpanIDs map[string]time.Time
	renderCache      map[spanRenderCacheKey]string
	conn             sse.ConnectionState
	paused           *atomic.Bool
	width            int
	height           int
	ready            bool
	repaintPending   bool
	stickToBottom    bool
	now              time.Time
}

func run(ctx context.Context, records <-chan sse.RecordMessage, states <-chan sse.ConnectionState, storeOptions state.StoreOptions) error {
	if !term.IsTerminal(os.Stdout.Fd()) || !term.IsTerminal(os.Stdin.Fd()) {
		return errNoTTY
	}
	paused := &atomic.Bool{}
	m := newModel(paused, storeOptions)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	go func() {
		for {
			if paused.Load() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(50 * time.Millisecond):
				}
				continue
			}
			select {
			case <-ctx.Done():
				return
			case record, ok := <-records:
				if !ok {
					return
				}
				if paused.Load() {
					continue
				}
				p.Send(eventMsg(record))
			}
		}
	}()
	go func() {
		for state := range states {
			p.Send(stateMsg(state))
		}
	}()
	_, err := p.Run()
	return err
}

func newModel(paused *atomic.Bool, storeOptions state.StoreOptions) *model {
	s := spinner.New()
	s.Spinner = spinner.MiniDot
	s.Spinner.FPS = 100 * time.Millisecond
	s.Style = runStyle
	h := help.New()
	h.ShortSeparator = " · "
	return &model{
		store:            state.NewStoreWithOptions(storeOptions),
		staleOpenSpanIDs: make(map[string]time.Time),
		renderCache:      make(map[spanRenderCacheKey]string),
		spinner:          s,
		retryTimer:       timer.NewWithInterval(0, 100*time.Millisecond),
		help:             h,
		paused:           paused,
		now:              time.Now(),
	}
}

func (m *model) Init() tea.Cmd { return m.spinner.Tick }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !m.ready {
			m.vp = viewport.New(msg.Width, msg.Height-2)
			m.ready = true
		} else {
			m.vp.Width = msg.Width
			m.vp.Height = msg.Height - 2
		}
		if m.width != msg.Width {
			m.renderCache = make(map[spanRenderCacheKey]string)
		}
		m.help.Width = msg.Width
		m.width = msg.Width
		m.height = msg.Height
		m.repaint()
		return m, nil
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Top):
			m.vp.GotoTop()
		case key.Matches(msg, keys.Bottom):
			m.vp.GotoBottom()
		case key.Matches(msg, keys.Pause):
			m.togglePaused()
			m.repaint()
			return m, nil
		case key.Matches(msg, keys.Clear):
			m.store.Clear()
			m.staleOpenSpanIDs = make(map[string]time.Time)
			m.renderCache = make(map[spanRenderCacheKey]string)
			m.repaint()
			return m, nil
		case key.Matches(msg, keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			m.repaint()
			return m, nil
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.now = time.Now()
		if m.hasOpenSpans() {
			m.repaint()
		}
		return m, cmd
	case eventMsg:
		if m.isPaused() {
			return m, nil
		}
		if msg.Record != nil {
			if m.vp.AtBottom() {
				m.stickToBottom = true
			}
			m.store.Apply(msg.Record)
			m.store.LimitRoots(maxRoots)
			m.pruneStaleOpenSpanIDs()
			return m, m.scheduleRepaint()
		}
		return m, nil
	case repaintMsg:
		m.repaintPending = false
		m.repaint()
		if m.stickToBottom {
			m.vp.GotoBottom()
			m.stickToBottom = false
		}
		return m, nil
	case stateMsg:
		m.conn = sse.ConnectionState(msg)
		now := time.Now()
		switch m.conn.State {
		case sse.StateConnected:
			m.conn.Err = ""
			m.repaint()
			return m, m.retryTimer.Stop()
		case sse.StateRetrying:
			m.markOpenSpansStale(now)
			remaining := time.Until(m.conn.RetryAt)
			if remaining < 0 {
				remaining = 0
			}
			m.retryTimer = timer.NewWithInterval(remaining.Truncate(time.Second), time.Second)
			m.repaint()
			return m, m.retryTimer.Init()
		case sse.StateDisconnected:
			m.markOpenSpansStale(now)
			m.repaint()
			return m, m.retryTimer.Stop()
		}
		m.repaint()
		return m, nil
	case timer.TickMsg, timer.StartStopMsg, timer.TimeoutMsg:
		var cmd tea.Cmd
		m.retryTimer, cmd = m.retryTimer.Update(msg)
		m.now = time.Now()
		m.repaint()
		return m, cmd
	}
	return m, nil
}

func (m *model) View() string {
	if !m.ready {
		return ""
	}
	footer := renderFooter(m.width, len(m.store.Roots()), m.conn, m.retryTimer, m.isPaused(), m.help.View(keys))
	return lipgloss.JoinVertical(lipgloss.Left, renderHeader(m.width), m.vp.View(), footer)
}

func (m *model) togglePaused() {
	if !m.isPaused() {
		m.paused.Store(true)
		m.store.ClearOpenSpans()
		m.staleOpenSpanIDs = make(map[string]time.Time)
		m.renderCache = make(map[spanRenderCacheKey]string)
		m.pruneStaleOpenSpanIDs()
		return
	}
	m.paused.Store(false)
}

func (m *model) isPaused() bool {
	return m.paused != nil && m.paused.Load()
}

func (m *model) repaint() {
	if !m.ready {
		return
	}
	m.vp.SetContent(renderRoots(m.store.Roots(), max(40, m.width-2), m.spinner.View(), m.now, m.staleOpenSpanIDs, m.renderCache))
}

func (m *model) scheduleRepaint() tea.Cmd {
	if m.repaintPending {
		return nil
	}
	m.repaintPending = true
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg {
		return repaintMsg{}
	})
}

func (m *model) markOpenSpansStale(at time.Time) {
	for _, root := range m.store.Roots() {
		markOpenSpanStale(root, at, m.staleOpenSpanIDs)
	}
}

func markOpenSpanStale(span *state.SpanState, at time.Time, stale map[string]time.Time) {
	if span == nil {
		return
	}
	if !span.Ended {
		if _, ok := stale[span.SpanID]; !ok {
			stale[span.SpanID] = at
		}
	}
	for _, child := range span.Children {
		markOpenSpanStale(child, at, stale)
	}
}

func (m *model) pruneStaleOpenSpanIDs() {
	if len(m.staleOpenSpanIDs) == 0 {
		return
	}
	current := make(map[string]struct{})
	for _, root := range m.store.Roots() {
		collectSpanIDs(root, current)
	}
	for spanID := range m.staleOpenSpanIDs {
		if _, ok := current[spanID]; !ok {
			delete(m.staleOpenSpanIDs, spanID)
		}
	}
}

func collectSpanIDs(span *state.SpanState, ids map[string]struct{}) {
	if span == nil {
		return
	}
	ids[span.SpanID] = struct{}{}
	for _, child := range span.Children {
		collectSpanIDs(child, ids)
	}
}

func (m *model) hasOpenSpans() bool {
	for _, root := range m.store.Roots() {
		if hasOpenSpan(root) {
			return true
		}
	}
	return false
}

func hasOpenSpan(span *state.SpanState) bool {
	if span == nil {
		return false
	}
	if !span.Ended {
		return true
	}
	for _, child := range span.Children {
		if hasOpenSpan(child) {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
