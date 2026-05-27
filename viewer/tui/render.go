package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/timer"
	"github.com/charmbracelet/lipgloss"
	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

var (
	headerStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("63")).Padding(0, 1)
	footerKey       = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#909090", Dark: "#626262"})
	footerDesc      = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#B2B2B2", Dark: "#4A4A4A"})
	footerSeparator = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#DDDADA", Dark: "#3C3C3C"})
	dimStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	labelStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true)
	okStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("78")).Bold(true)
	errStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	runStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
)

func renderHeader(width int) string {
	return headerStyle.Width(width).Render(" sap · live monitor ")
}

func renderFooter(width, roots int, conn sse.ConnectionState, retry timer.Model, paused bool, helpView string) string {
	separator := footerSeparator.Render(" · ")
	left := footerItem("roots", fmt.Sprintf("%d", roots)) + separator + connectionStatus(conn, retry)
	if paused {
		left += separator + runStyle.Render("paused")
	}
	right := helpView
	pad := width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

func footerItem(key, desc string) string {
	return footerKey.Render(key) + " " + footerDesc.Render(desc)
}

func connectionStatus(conn sse.ConnectionState, retry timer.Model) string {
	switch conn.State {
	case sse.StateConnected:
		return okStyle.Render("●")
	case sse.StateConnecting:
		return runStyle.Render("●")
	case sse.StateRetrying:
		return runStyle.Render("●") + " " + footerDesc.Render(retry.View())
	default:
		return dimStyle.Render("●")
	}
}

type spanRenderCacheKey struct {
	spanID string
	depth  int
	width  int
}

func renderRoots(roots []*state.SpanState, width int, spin string, now time.Time, staleOpenSpanIDs map[string]time.Time, cache map[spanRenderCacheKey]string) string {
	var b strings.Builder
	for i, root := range roots {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(renderSpan(root, 0, width, spin, now, staleOpenSpanIDs, cache))
	}
	return b.String()
}

func renderSpan(span *state.SpanState, depth, width int, spin string, now time.Time, staleOpenSpanIDs map[string]time.Time, cache map[spanRenderCacheKey]string) string {
	cacheable := isClosedSpanTree(span)
	if cacheable && cache != nil {
		key := spanRenderCacheKey{spanID: span.SpanID, depth: depth, width: width}
		if rendered, ok := cache[key]; ok {
			return rendered
		}
	}
	inner := width - 4
	if inner < 24 {
		inner = 24
	}
	color := lipgloss.Color([]string{"213", "141", "117", "220", "78", "209"}[depth%6])
	title := lipgloss.NewStyle().Foreground(color).Bold(true).Render(span.Name)
	headerLeft := title
	headerRight := statusView(span, spin, staleOpenSpanIDs)
	if timing := timingView(span, now, staleOpenSpanIDs); timing != "" {
		headerRight = dimStyle.Render(timing) + " " + headerRight
	}
	header := headerLeft + strings.Repeat(" ", max(1, inner-lipgloss.Width(headerLeft)-lipgloss.Width(headerRight))) + headerRight
	parts := []string{header}
	if span.ErrorMessage != "" {
		parts = append(parts, dimStyle.Render(strings.Repeat("─", inner)), renderError(span.ErrorMessage, inner))
	}
	attrs := renderAttributes(span.Attributes, inner)
	if attrs != "" {
		parts = append(parts, dimStyle.Render(strings.Repeat("─", inner)), attrs)
	}
	if events := renderEvents(span.Events, inner, span.StartedAt); events != "" {
		parts = append(parts, dimStyle.Render(strings.Repeat("─", inner)), events)
	}
	for _, child := range span.Children {
		parts = append(parts, renderSpan(child, depth+1, inner, spin, now, staleOpenSpanIDs, cache))
	}
	rendered := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).Padding(0, 1).Width(width - 2).Render(strings.Join(parts, "\n"))
	if cacheable && cache != nil {
		key := spanRenderCacheKey{spanID: span.SpanID, depth: depth, width: width}
		cache[key] = rendered
	}
	return rendered
}

func renderError(message string, width int) string {
	return lipgloss.NewStyle().Width(width).Render(errStyle.Render("error: ") + message)
}

func isClosedSpanTree(span *state.SpanState) bool {
	if span == nil || !span.Ended {
		return false
	}
	for _, child := range span.Children {
		if !isClosedSpanTree(child) {
			return false
		}
	}
	return true
}

func statusView(span *state.SpanState, spin string, staleOpenSpanIDs map[string]time.Time) string {
	if !span.Ended {
		if _, stale := staleOpenSpanIDs[span.SpanID]; stale {
			return errStyle.Render("✕")
		}
		return spin
	}
	if span.ErrorMessage != "" {
		return errStyle.Render("✕")
	}
	return okStyle.Render("✓")
}

func renderAttributes(attrs []*sapv1.Attribute, width int) string {
	var parts []string
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		label := attr.GetKey()
		if attr.GetLabel() != "" {
			label = attr.GetLabel()
		}
		lang := codeLanguage(attr.GetDisplayHints())
		if lang != "" || hasHint(attr.GetDisplayHints(), "code") {
			parts = append(parts, labelStyle.Render(label+":")+"\n"+highlightCode(lang, attr.GetValue()))
			continue
		}
		parts = append(parts, lipgloss.NewStyle().Width(width).Render(labelStyle.Render(label+": ")+attr.GetValue()))
	}
	return strings.Join(parts, "\n")
}

func renderEvents(events []*state.EventState, width int, startedAt time.Time) string {
	var parts []string
	for _, event := range events {
		headerLeft := dimStyle.Render("* ") + event.Name
		header := headerLeft
		if offset := eventOffset(startedAt, event.At); offset != "" {
			headerRight := dimStyle.Render(offset)
			header = headerLeft + strings.Repeat(" ", max(1, width-lipgloss.Width(headerLeft)-lipgloss.Width(headerRight))) + headerRight
		}

		eventParts := []string{lipgloss.NewStyle().Width(width).Render(header)}
		if attrs := renderAttributes(event.Attributes, max(1, width-2)); attrs != "" {
			eventParts = append(eventParts, indentLines(attrs, "  "))
		}
		parts = append(parts, strings.Join(eventParts, "\n"))
	}
	return strings.Join(parts, "\n")
}

func indentLines(value, prefix string) string {
	if value == "" {
		return ""
	}
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func timingView(span *state.SpanState, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	if span.StartedAt.IsZero() {
		return ""
	}
	if span.Ended && !span.EndedAt.IsZero() {
		return spanTimerView(span.EndedAt.Sub(span.StartedAt))
	}
	if staleAt, stale := staleOpenSpanIDs[span.SpanID]; stale {
		return spanTimerView(staleAt.Sub(span.StartedAt))
	}
	return spanTimerView(now.Sub(span.StartedAt))
}

func spanTimerView(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	component := timer.NewWithInterval(d.Truncate(time.Millisecond), time.Millisecond)
	return component.View()
}

func eventOffset(startedAt, eventAt time.Time) string {
	if startedAt.IsZero() || eventAt.IsZero() {
		return ""
	}
	return "+" + formatDuration(eventAt.Sub(startedAt))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return d.Round(10 * time.Millisecond).String()
	}
	if d < time.Minute {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}

func inlineAttrs(attrs []*sapv1.Attribute) string {
	parts := make([]string, 0, len(attrs))
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", attr.GetKey(), attr.GetValue()))
	}
	return strings.Join(parts, " ")
}

func hasHint(hints []string, want string) bool {
	for _, hint := range hints {
		if hint == want {
			return true
		}
	}
	return false
}
