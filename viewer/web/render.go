package main

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

func renderPage(w io.Writer, snap snapshot) error {
	return Page(snap).Render(context.Background(), w)
}

func renderRoots(roots []*state.SpanState, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	return renderToString(Roots(roots, now, staleOpenSpanIDs))
}

func renderSpanPatch(span *state.SpanState, depth int, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	return renderToString(SpanPatch(span, depth, now, staleOpenSpanIDs))
}

func renderSpanSummaryPatch(span *state.SpanState, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	return renderToString(SpanSummaryPatch(span, now, staleOpenSpanIDs))
}

func renderSpanAttributesPatch(span *state.SpanState) string {
	return renderToString(SpanAttributesPatch(span))
}

func renderSpanTimelinePatch(span *state.SpanState, depth int, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	return renderToString(SpanTimelinePatch(span, depth, now, staleOpenSpanIDs))
}

func renderTimelineAppendPatch(parent *state.SpanState, item timelineItem, depth int, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	return renderToString(TimelineAppendPatch(parent, item, depth, now, staleOpenSpanIDs))
}

func renderStatus(roots int, conn sse.ConnectionState, paused bool) string {
	return renderToString(Status(roots, conn, paused))
}

func renderToString(component templ.Component) string {
	var b strings.Builder
	if err := component.Render(context.Background(), &b); err != nil {
		return fmt.Sprintf(`<section class="empty"><div><strong>render error</strong><br><span>%s</span></div></section>`, html.EscapeString(err.Error()))
	}
	return b.String()
}

func spanHue(depth int) int {
	return []int{310, 265, 25, 48, 136, 195}[depth%6]
}

func statusToneClass(span *state.SpanState, staleOpenSpanIDs map[string]time.Time) templ.CSSClass {
	if span == nil {
		return templ.ComponentCSSClass{}
	}
	if !span.Ended {
		if _, stale := staleOpenSpanIDs[span.SpanID]; stale {
			return statusError()
		}
		return templ.ComponentCSSClass{}
	}
	if span.ErrorMessage != "" {
		return statusError()
	}
	return statusOK()
}

func statusIcon(span *state.SpanState, staleOpenSpanIDs map[string]time.Time) string {
	if span == nil {
		return ""
	}
	if !span.Ended {
		if _, stale := staleOpenSpanIDs[span.SpanID]; stale {
			return "✕"
		}
		return ""
	}
	if span.ErrorMessage != "" {
		return "✕"
	}
	return "✓"
}

func statusText(span *state.SpanState, staleOpenSpanIDs map[string]time.Time) string {
	if span == nil {
		return "unknown"
	}
	if !span.Ended {
		if _, stale := staleOpenSpanIDs[span.SpanID]; stale {
			return "stale"
		}
		return "running"
	}
	if span.ErrorMessage != "" {
		return "error"
	}
	return "complete"
}

func hasTimer(span *state.SpanState) bool {
	return span != nil && !span.StartedAt.IsZero()
}

func timerXData(span *state.SpanState, staleOpenSpanIDs map[string]time.Time) string {
	return fmt.Sprintf("sapTimer(%d,%d)", timerStartMS(span), timerEndMS(span, staleOpenSpanIDs))
}

func timerStartMS(span *state.SpanState) int64 {
	if span == nil || span.StartedAt.IsZero() {
		return 0
	}
	return span.StartedAt.UnixMilli()
}

func timerEndMS(span *state.SpanState, staleOpenSpanIDs map[string]time.Time) int64 {
	if span == nil {
		return 0
	}
	if span.Ended && !span.EndedAt.IsZero() {
		return span.EndedAt.UnixMilli()
	}
	if staleAt, stale := staleOpenSpanIDs[span.SpanID]; stale {
		return staleAt.UnixMilli()
	}
	return 0
}

func timingView(span *state.SpanState, now time.Time, staleOpenSpanIDs map[string]time.Time) string {
	if !hasTimer(span) {
		return ""
	}
	endMS := timerEndMS(span, staleOpenSpanIDs)
	if endMS > 0 {
		return formatDuration(time.Duration(endMS-timerStartMS(span)) * time.Millisecond)
	}
	return formatDuration(now.Sub(span.StartedAt))
}

type timelineItem struct {
	Kind  string
	At    time.Time
	Event *state.EventState
	Span  *state.SpanState
	index int
}

func timelineItems(span *state.SpanState) []timelineItem {
	if span == nil {
		return nil
	}
	items := make([]timelineItem, 0, len(span.Events)+len(span.Children))
	for i, event := range span.Events {
		if event == nil {
			continue
		}
		items = append(items, timelineItem{Kind: "event", At: event.At, Event: event, index: i})
	}
	base := len(items)
	for i, child := range span.Children {
		if child == nil {
			continue
		}
		items = append(items, timelineItem{Kind: "span", At: child.StartedAt, Span: child, index: base + i})
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.At.IsZero() && right.At.IsZero() {
			return left.index < right.index
		}
		if left.At.IsZero() {
			return false
		}
		if right.At.IsZero() {
			return true
		}
		if left.At.Equal(right.At) {
			return left.index < right.index
		}
		return left.At.Before(right.At)
	})
	return items
}

func timelineOffset(parent *state.SpanState, item timelineItem) string {
	if parent == nil || parent.StartedAt.IsZero() || item.At.IsZero() {
		return ""
	}
	return "+" + formatDuration(item.At.Sub(parent.StartedAt))
}

func spanCardKey(span *state.SpanState) string {
	if span == nil {
		return ""
	}
	if span.SpanID != "" {
		return "span:" + span.SpanID
	}
	return fmt.Sprintf("span:%s:%s:%d", span.TraceID, span.Name, span.StartedAt.UnixNano())
}

func spanDOMID(span *state.SpanState) string {
	return safeCSSIdent(spanCardKey(span), "sap-span-unknown")
}

func spanSummaryDOMID(span *state.SpanState) string {
	return safeCSSIdent("summary:"+spanCardKey(span), "sap-span-summary-unknown")
}

func spanAttributesDOMID(span *state.SpanState) string {
	return safeCSSIdent("attrs:"+spanCardKey(span), "sap-span-attrs-unknown")
}

func spanTimelineDOMID(span *state.SpanState) string {
	return safeCSSIdent("timeline:"+spanCardKey(span), "sap-span-timeline-unknown")
}

func timelineListDOMID(span *state.SpanState) string {
	return safeCSSIdent("timeline-list:"+spanCardKey(span), "sap-span-timeline-list-unknown")
}

func attrDOMID(ownerKey string, attr *sapv1.Attribute, index int) string {
	key := ""
	if attr != nil {
		key = attr.GetKey()
	}
	return safeCSSIdent(fmt.Sprintf("attr:%s:%s:%d", ownerKey, key, index), "sap-attr-unknown")
}

func eventDOMID(key string) string {
	return safeCSSIdent(key, "sap-event-unknown")
}

func spanStyle(span *state.SpanState, depth int) string {
	return fmt.Sprintf("--span-hue:%d", spanHue(depth))
}

func safeCSSIdent(value, fallback string) string {
	if value == "" {
		return fallback
	}
	var b strings.Builder
	b.WriteString("sap-")
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func eventCardKey(parent *state.SpanState, item timelineItem) string {
	parentID := ""
	if parent != nil {
		parentID = parent.SpanID
	}
	name := ""
	if item.Event != nil {
		name = item.Event.Name
	}
	return fmt.Sprintf("event:%s:%d:%d:%s", parentID, item.At.UnixNano(), item.index, name)
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

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func attrLabel(attr *sapv1.Attribute) string {
	if attr == nil {
		return ""
	}
	if attr.GetLabel() != "" {
		return attr.GetLabel()
	}
	return attr.GetKey()
}

func attrIsBadge(attr *sapv1.Attribute) bool {
	return attr != nil && hasHint(attr.GetDisplayHints(), "badge")
}

func attrIsCode(attr *sapv1.Attribute) bool {
	if attr == nil {
		return false
	}
	return codeLanguage(attr.GetDisplayHints()) != "" || hasHint(attr.GetDisplayHints(), "code")
}

func codeLanguage(hints []string) string {
	for _, hint := range hints {
		if strings.HasPrefix(hint, "code:") {
			return strings.TrimPrefix(hint, "code:")
		}
	}
	for _, hint := range hints {
		if hint == "code" {
			return "text"
		}
	}
	return ""
}

func hasHint(hints []string, want string) bool {
	for _, hint := range hints {
		if hint == want {
			return true
		}
	}
	return false
}

var highlightCache sync.Map

func highlightCodeHTML(lang, src string) string {
	cacheKey := lang + "\x00" + src
	if highlighted, ok := highlightCache.Load(cacheKey); ok {
		return highlighted.(string)
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Get("monokai")
	}
	if style == nil {
		style = styles.Fallback
	}
	formatter := chromahtml.New(chromahtml.WithClasses(false), chromahtml.PreventSurroundingPre(true))
	iterator, err := lexer.Tokenise(nil, src)
	if err != nil {
		return html.EscapeString(src)
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return html.EscapeString(src)
	}
	highlighted := strings.TrimRight(buf.String(), "\n")
	highlightCache.Store(cacheKey, highlighted)
	return highlighted
}

func connectionDotClass(conn sse.ConnectionState) templ.CSSClass {
	switch conn.State {
	case sse.StateConnected:
		return dotConnected()
	case sse.StateConnecting:
		return dotConnecting()
	case sse.StateRetrying:
		return dotRetrying()
	default:
		return dotDisconnected()
	}
}

func connectionLabel(conn sse.ConnectionState) string {
	if conn.State == "" {
		return sse.StateDisconnected
	}
	return conn.State
}

func connectionDetail(conn sse.ConnectionState) string {
	switch conn.State {
	case sse.StateConnecting:
		if conn.Attempt > 0 {
			return fmt.Sprintf("attempt %d", conn.Attempt)
		}
	case sse.StateRetrying:
		if conn.Err != "" {
			return conn.Err
		}
		if !conn.RetryAt.IsZero() {
			remaining := time.Until(conn.RetryAt).Round(time.Second)
			if remaining < 0 {
				remaining = 0
			}
			return "retry in " + remaining.String()
		}
	}
	return ""
}

func pauseButtonLabel(paused bool) string {
	if paused {
		return "Resume"
	}
	return "Pause"
}

func stylesheetClasses() []templ.CSSClass {
	return []templ.CSSClass{
		appRoot(), topBar(), brandTitle(), toolbarLabel(), pageMain(), footerDock(),
		statusBar(), statusGroup(), pill(), dot(), dotConnected(), dotConnecting(), dotRetrying(), dotDisconnected(),
		buttonBase(), dangerButton(), emptyState(), emptyTitle(), traceList(), spanCard(), spanChildren(),
		spanHeader(), spanTitle(), spanName(), spanMeta(), spanState(), statusOK(), statusError(), spinner(),
		sectionBlock(), sectionTitle(), attrsGrid(), attrRow(), attrLabelClass(), attrValueClass(), badge(),
		codeBlock(), codeText(), timelineList(), timelineItemClass(), eventCardClass(), eventHeaderClass(),
		eventOffsetClass(), errorMessage(),
	}
}
