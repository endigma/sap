// Package state maintains an in-memory view of Sap records.
package state

import (
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/proto"
)

// Store applies records to an in-memory span tree.
type Store struct {
	roots               []*SpanState
	byID                map[string]*SpanState
	staleOpenAt         map[string]time.Time
	allowUnknownParents bool
}

// StoreOptions configures how a Store reconstructs records.
type StoreOptions struct {
	// AllowUnknownParents promotes spans whose non-empty parent_span_id does not
	// refer to a span currently tracked by the store to roots. When false, those
	// spans are ignored.
	AllowUnknownParents bool
}

// SpanState is the current state of a span and its descendants.
type SpanState struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	StartedAt    time.Time
	EndedAt      time.Time
	Attributes   []*sapv1.Attribute
	Events       []*EventState
	Children     []*SpanState
	Ended        bool
	TerminalType sapv1.SpanEnded_TerminalType
	ErrorMessage string
}

// EventState is the current state of a span event.
type EventState struct {
	Name       string
	At         time.Time
	Attributes []*sapv1.Attribute
	Severity   sapv1.SpanEvent_Severity
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return NewStoreWithOptions(StoreOptions{})
}

// NewStoreWithOptions creates an empty Store using the supplied options.
func NewStoreWithOptions(options StoreOptions) *Store {
	return &Store{
		byID:                make(map[string]*SpanState),
		staleOpenAt:         make(map[string]time.Time),
		allowUnknownParents: options.AllowUnknownParents,
	}
}

// Apply updates the store with a record.
func (s *Store) Apply(record *sapv1.Record) {
	if record == nil {
		return
	}
	switch record.WhichKind() {
	case sapv1.Record_SpanStarted_case:
		s.applyStarted(record.GetSpanStarted())
	case sapv1.Record_SpanUpdated_case:
		s.applyUpdated(record.GetSpanUpdated())
	case sapv1.Record_SpanEnded_case:
		s.applyEnded(record.GetSpanEnded())
	case sapv1.Record_SpanEvent_case:
		s.applyEvent(record.GetSpanEvent())
	}
}

// Roots returns the root spans currently tracked by the store.
func (s *Store) Roots() []*SpanState {
	return s.roots
}

// Clear removes all tracked spans from the store.
func (s *Store) Clear() {
	s.roots = nil
	s.byID = make(map[string]*SpanState)
	s.staleOpenAt = make(map[string]time.Time)
}

// FindSpan returns the span with the given ID and its depth below its root,
// or nil when the ID is not tracked.
func (s *Store) FindSpan(spanID string) (*SpanState, int) {
	if spanID == "" {
		return nil, 0
	}
	for _, root := range s.roots {
		if span, depth := findSpan(root, spanID, 0); span != nil {
			return span, depth
		}
	}
	return nil, 0
}

func findSpan(span *SpanState, spanID string, depth int) (*SpanState, int) {
	if span == nil {
		return nil, 0
	}
	if span.SpanID == spanID {
		return span, depth
	}
	for _, child := range span.Children {
		if found, foundDepth := findSpan(child, spanID, depth+1); found != nil {
			return found, foundDepth
		}
	}
	return nil, 0
}

// HasOpenSpans reports whether any tracked span is still in progress.
func (s *Store) HasOpenSpans() bool {
	for _, root := range s.roots {
		if hasOpenSpan(root) {
			return true
		}
	}
	return false
}

func hasOpenSpan(span *SpanState) bool {
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

// MarkOpenSpansStale marks every currently open span as stale at the given
// time, e.g. when the record stream disconnects. Spans already marked keep
// their original time; marks are dropped when the span leaves the store.
func (s *Store) MarkOpenSpansStale(at time.Time) {
	for _, root := range s.roots {
		s.markOpenSpanStale(root, at)
	}
}

func (s *Store) markOpenSpanStale(span *SpanState, at time.Time) {
	if span == nil {
		return
	}
	if !span.Ended {
		if _, ok := s.staleOpenAt[span.SpanID]; !ok {
			s.staleOpenAt[span.SpanID] = at
		}
	}
	for _, child := range span.Children {
		s.markOpenSpanStale(child, at)
	}
}

// StaleOpenSpanIDs returns the stale-mark times by span ID. The map is owned
// by the store; callers must not mutate it.
func (s *Store) StaleOpenSpanIDs() map[string]time.Time {
	return s.staleOpenAt
}

// ClearOpenSpans removes all currently in-progress spans. If an open span has
// descendants, the entire subtree is removed because descendants cannot be
// meaningfully re-parented once their parent span is discarded.
func (s *Store) ClearOpenSpans() {
	if len(s.roots) == 0 {
		return
	}
	kept := make([]*SpanState, 0, len(s.roots))
	for _, root := range s.roots {
		if s.keepClosedSpan(root) {
			kept = append(kept, root)
		}
	}
	s.roots = kept
}

func (s *Store) keepClosedSpan(span *SpanState) bool {
	if span == nil {
		return false
	}
	if !span.Ended {
		s.deleteSpan(span)
		return false
	}
	keptChildren := make([]*SpanState, 0, len(span.Children))
	for _, child := range span.Children {
		if s.keepClosedSpan(child) {
			keptChildren = append(keptChildren, child)
		}
	}
	span.Children = keptChildren
	return true
}

// LimitRoots keeps only the newest limit root spans, discarding older roots
// and their descendants in FIFO order.
func (s *Store) LimitRoots(limit int) {
	if limit <= 0 {
		s.Clear()
		return
	}
	if len(s.roots) <= limit {
		return
	}
	remove := len(s.roots) - limit
	for _, root := range s.roots[:remove] {
		s.deleteSpan(root)
	}
	s.roots = append([]*SpanState(nil), s.roots[remove:]...)
}

func (s *Store) deleteSpan(span *SpanState) {
	if span == nil {
		return
	}
	delete(s.byID, span.SpanID)
	delete(s.staleOpenAt, span.SpanID)
	for _, child := range span.Children {
		s.deleteSpan(child)
	}
}

func (s *Store) applyStarted(started *sapv1.SpanStarted) {
	if started == nil || started.GetSpanId() == "" {
		return
	}
	spanID := started.GetSpanId()
	if _, exists := s.byID[spanID]; exists {
		return
	}
	parentID := started.GetParentSpanId()
	parent, parentKnown := s.byID[parentID]
	if parentID != "" && !parentKnown && !s.allowUnknownParents {
		return
	}
	span := &SpanState{
		TraceID:      started.GetTraceId(),
		SpanID:       spanID,
		ParentSpanID: parentID,
		Name:         started.GetName(),
		StartedAt:    timeValue(started.GetStartedAt()),
		Attributes:   cloneAttributes(started.GetAttributes()),
	}
	s.byID[spanID] = span
	if parentKnown {
		parent.Children = append(parent.Children, span)
	} else {
		s.roots = append(s.roots, span)
	}
}

func (s *Store) applyUpdated(updated *sapv1.SpanUpdated) {
	if updated == nil {
		return
	}
	span, ok := s.byID[updated.GetSpanId()]
	if !ok || span.Ended {
		return
	}
	for _, incoming := range updated.GetAttributes() {
		if incoming == nil || incoming.GetKey() == "" {
			continue
		}
		matched := false
		for i, existing := range span.Attributes {
			if existing != nil && existing.GetKey() == incoming.GetKey() {
				span.Attributes[i] = cloneAttributes([]*sapv1.Attribute{incoming})[0]
				matched = true
				break
			}
		}
		if !matched {
			span.Attributes = append(span.Attributes, cloneAttributes([]*sapv1.Attribute{incoming})[0])
		}
	}
}

func (s *Store) applyEnded(ended *sapv1.SpanEnded) {
	if ended == nil {
		return
	}
	span, ok := s.byID[ended.GetSpanId()]
	if !ok || span.Ended {
		return
	}
	span.Ended = true
	span.EndedAt = timeValue(ended.GetEndedAt())
	span.TerminalType = ended.GetTerminalType()
	span.ErrorMessage = ended.GetErrorMessage()
}

func (s *Store) applyEvent(event *sapv1.SpanEvent) {
	if event == nil {
		return
	}
	span, ok := s.byID[event.GetSpanId()]
	if !ok || span.Ended {
		return
	}
	span.Events = append(span.Events, &EventState{Name: event.GetName(), At: timeValue(event.GetEventAt()), Attributes: cloneAttributes(event.GetAttributes()), Severity: event.GetSeverity()})
}

func timeValue(ts interface{ AsTime() time.Time }) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

func cloneAttributes(attrs []*sapv1.Attribute) []*sapv1.Attribute {
	if len(attrs) == 0 {
		return nil
	}
	cloned := make([]*sapv1.Attribute, 0, len(attrs))
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		cloned = append(cloned, proto.CloneOf(attr))
	}
	return cloned
}
