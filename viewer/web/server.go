package main

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

type snapshot struct {
	RootsHTML  string
	PatchHTML  string
	StatusHTML string
}

type server struct {
	mu               sync.RWMutex
	store            *state.Store
	staleOpenSpanIDs map[string]time.Time
	conn             sse.ConnectionState
	paused           bool
	maxRoots         int
	subscribers      map[chan snapshot]struct{}
}

func newServer(maxRoots int, storeOptions state.StoreOptions) *server {
	if maxRoots <= 0 {
		maxRoots = 50
	}
	return &server{
		store:            state.NewStoreWithOptions(storeOptions),
		staleOpenSpanIDs: make(map[string]time.Time),
		maxRoots:         maxRoots,
		subscribers:      make(map[chan snapshot]struct{}),
	}
}

func (s *server) start(ctx context.Context, records <-chan sse.RecordMessage, states <-chan sse.ConnectionState) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case record, ok := <-records:
				if !ok {
					records = nil
					continue
				}
				s.applyRecord(record)
			case state, ok := <-states:
				if !ok {
					states = nil
					continue
				}
				s.setConnectionState(state)
			}
		}
	}()
}

func (s *server) mount(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleHome)
	mux.HandleFunc("/stream", s.handleStream)
	mux.HandleFunc("/pause", s.handlePause)
	mux.HandleFunc("/clear", s.handleClear)
}

func (s *server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := renderPage(w, s.snapshot()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *server) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported by responseWriter", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	snapshots, unsubscribe := s.subscribe()
	defer unsubscribe()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case snap, ok := <-snapshots:
			if !ok {
				return
			}
			if snap.RootsHTML != "" {
				if err := writeSSE(w, "roots", snap.RootsHTML); err != nil {
					return
				}
			}
			if snap.PatchHTML != "" {
				if err := writeSSE(w, "patch", snap.PatchHTML); err != nil {
					return
				}
			}
			if snap.StatusHTML != "" {
				if err := writeSSE(w, "status", snap.StatusHTML); err != nil {
					return
				}
			}
			flusher.Flush()
		}
	}
}

func (s *server) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	snap := s.togglePaused()
	_, _ = w.Write([]byte(snap.StatusHTML))
}

func (s *server) handleClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	snap := s.clear()
	_, _ = w.Write([]byte(snap.StatusHTML))
}

func (s *server) subscribe() (<-chan snapshot, func()) {
	ch := make(chan snapshot, 8)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	ch <- s.snapshotLocked(time.Now())
	s.mu.Unlock()

	return ch, func() {
		s.mu.Lock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
}

func (s *server) snapshot() snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshotLocked(time.Now())
}

func (s *server) applyRecord(msg sse.RecordMessage) {
	if msg.Record == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.paused {
		return
	}
	beforeRootIDs := s.rootIDsLocked()
	spanID := recordSpanID(msg.Record)
	s.store.Apply(msg.Record)
	s.store.LimitRoots(s.maxRoots)
	s.pruneStaleOpenSpanIDsLocked()
	now := time.Now()
	if !sameStrings(beforeRootIDs, s.rootIDsLocked()) {
		s.broadcastLocked(s.snapshotLocked(now))
		return
	}
	snap, ok := s.recordPatchSnapshotLocked(msg.Record, spanID, now)
	if !ok {
		s.broadcastLocked(s.snapshotLocked(now))
		return
	}
	s.broadcastLocked(snap)
}

func (s *server) setConnectionState(conn sse.ConnectionState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conn = conn
	now := time.Now()
	switch conn.State {
	case sse.StateConnected:
		s.conn.Err = ""
		s.broadcastLocked(s.statusSnapshotLocked())
	case sse.StateRetrying, sse.StateDisconnected:
		s.markOpenSpansStaleLocked(now)
		s.broadcastLocked(s.snapshotLocked(now))
	default:
		s.broadcastLocked(s.statusSnapshotLocked())
	}
}

func (s *server) togglePaused() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.paused {
		s.paused = true
		s.store.ClearOpenSpans()
		s.staleOpenSpanIDs = make(map[string]time.Time)
		s.pruneStaleOpenSpanIDsLocked()
	} else {
		s.paused = false
	}
	return s.broadcastLocked(s.snapshotLocked(time.Now()))
}

func (s *server) clear() snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.store.Clear()
	s.staleOpenSpanIDs = make(map[string]time.Time)
	return s.broadcastLocked(s.snapshotLocked(time.Now()))
}

func (s *server) broadcastLocked(snap snapshot) snapshot {
	for ch := range s.subscribers {
		select {
		case ch <- snap:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snap:
			default:
			}
		}
	}
	return snap
}

func (s *server) snapshotLocked(now time.Time) snapshot {
	return snapshot{
		RootsHTML:  renderRoots(s.store.Roots(), now, s.staleOpenSpanIDs),
		StatusHTML: renderStatus(len(s.store.Roots()), s.conn, s.paused),
	}
}

func (s *server) patchSnapshotLocked(html string) snapshot {
	return snapshot{
		PatchHTML:  html,
		StatusHTML: renderStatus(len(s.store.Roots()), s.conn, s.paused),
	}
}

func (s *server) statusSnapshotLocked() snapshot {
	return snapshot{StatusHTML: renderStatus(len(s.store.Roots()), s.conn, s.paused)}
}

func (s *server) rootIDsLocked() []string {
	roots := s.store.Roots()
	ids := make([]string, 0, len(roots))
	for _, root := range roots {
		if root == nil {
			ids = append(ids, "")
			continue
		}
		ids = append(ids, root.SpanID)
	}
	return ids
}

func (s *server) recordPatchSnapshotLocked(record *sapv1.Record, spanID string, now time.Time) (snapshot, bool) {
	if record == nil {
		return snapshot{}, false
	}
	switch record.WhichKind() {
	case sapv1.Record_SpanStarted_case:
		started := record.GetSpanStarted()
		if started.GetParentSpanId() == "" {
			return snapshot{}, false
		}
		parent, depth := s.findSpanByIDLocked(started.GetParentSpanId())
		if parent == nil {
			return snapshot{}, false
		}
		if len(timelineItems(parent)) <= 1 {
			return s.patchSnapshotLocked(renderSpanTimelinePatch(parent, depth, now, s.staleOpenSpanIDs)), true
		}
		child, childIndex := childSpanByID(parent, spanID)
		if child == nil {
			return s.patchSnapshotLocked(renderSpanTimelinePatch(parent, depth, now, s.staleOpenSpanIDs)), true
		}
		item := timelineItem{Kind: "span", At: child.StartedAt, Span: child, index: len(parent.Events) + childIndex}
		return s.patchSnapshotLocked(renderTimelineAppendPatch(parent, item, depth, now, s.staleOpenSpanIDs)), true
	case sapv1.Record_SpanUpdated_case:
		span, _ := s.findSpanByIDLocked(spanID)
		if span == nil {
			return snapshot{}, false
		}
		return s.patchSnapshotLocked(renderSpanAttributesPatch(span)), true
	case sapv1.Record_SpanEvent_case:
		span, depth := s.findSpanByIDLocked(spanID)
		if span == nil {
			return snapshot{}, false
		}
		if len(timelineItems(span)) <= 1 || len(span.Events) == 0 {
			return s.patchSnapshotLocked(renderSpanTimelinePatch(span, depth, now, s.staleOpenSpanIDs)), true
		}
		eventIndex := len(span.Events) - 1
		event := span.Events[eventIndex]
		item := timelineItem{Kind: "event", At: event.At, Event: event, index: eventIndex}
		return s.patchSnapshotLocked(renderTimelineAppendPatch(span, item, depth, now, s.staleOpenSpanIDs)), true
	case sapv1.Record_SpanEnded_case:
		span, _ := s.findSpanByIDLocked(spanID)
		if span == nil {
			return snapshot{}, false
		}
		return s.patchSnapshotLocked(renderSpanSummaryPatch(span, now, s.staleOpenSpanIDs)), true
	default:
		span, depth := s.findSpanByIDLocked(spanID)
		if span == nil {
			return snapshot{}, false
		}
		return s.patchSnapshotLocked(renderSpanPatch(span, depth, now, s.staleOpenSpanIDs)), true
	}
}

func (s *server) findSpanByIDLocked(spanID string) (*state.SpanState, int) {
	if spanID == "" {
		return nil, 0
	}
	for _, root := range s.store.Roots() {
		if span, depth := findSpanByID(root, spanID, 0); span != nil {
			return span, depth
		}
	}
	return nil, 0
}

func findSpanByID(span *state.SpanState, spanID string, depth int) (*state.SpanState, int) {
	if span == nil {
		return nil, 0
	}
	if span.SpanID == spanID {
		return span, depth
	}
	for _, child := range span.Children {
		if found, foundDepth := findSpanByID(child, spanID, depth+1); found != nil {
			return found, foundDepth
		}
	}
	return nil, 0
}

func childSpanByID(parent *state.SpanState, spanID string) (*state.SpanState, int) {
	if parent == nil {
		return nil, 0
	}
	for i, child := range parent.Children {
		if child != nil && child.SpanID == spanID {
			return child, i
		}
	}
	return nil, 0
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func recordSpanID(record *sapv1.Record) string {
	if record == nil {
		return ""
	}
	switch record.WhichKind() {
	case sapv1.Record_SpanStarted_case:
		return record.GetSpanStarted().GetSpanId()
	case sapv1.Record_SpanUpdated_case:
		return record.GetSpanUpdated().GetSpanId()
	case sapv1.Record_SpanEnded_case:
		return record.GetSpanEnded().GetSpanId()
	case sapv1.Record_SpanEvent_case:
		return record.GetSpanEvent().GetSpanId()
	default:
		return ""
	}
}

func (s *server) markOpenSpansStaleLocked(at time.Time) {
	for _, root := range s.store.Roots() {
		markOpenSpanStale(root, at, s.staleOpenSpanIDs)
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

func (s *server) pruneStaleOpenSpanIDsLocked() {
	if len(s.staleOpenSpanIDs) == 0 {
		return
	}
	current := make(map[string]struct{})
	for _, root := range s.store.Roots() {
		collectSpanIDs(root, current)
	}
	for spanID := range s.staleOpenSpanIDs {
		if _, ok := current[spanID]; !ok {
			delete(s.staleOpenSpanIDs, spanID)
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

func writeSSE(w http.ResponseWriter, event, data string) error {
	if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	if data == "" {
		_, err := fmt.Fprint(w, "data:\n\n")
		return err
	}

	scanner := bufio.NewScanner(strings.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if _, err := fmt.Fprintf(w, "data: %s\n", scanner.Text()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	_, err := fmt.Fprint(w, "\n")
	return err
}
