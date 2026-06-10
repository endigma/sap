package sap

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type contextKey struct{}

type hubContextKey struct{}

// Span tracks a timed unit of work and emits lifecycle records.
// All methods are safe to call on a nil *Span; they are no-ops.
type Span struct {
	mu       sync.Mutex
	hub      *Hub
	traceID  string
	spanID   string
	parentID string
	ended    bool
}

// Start starts a span on the hub and returns a context containing both the
// span and the hub, so downstream code can start child spans with the
// package-level Start without holding a *Hub. Spans always start at
// time.Now(); back-dating is deliberately unsupported.
func (h *Hub) Start(ctx context.Context, name string, attrs ...*Attribute) (context.Context, *Span) {
	return startWithHub(ctx, h, name, attrs...)
}

// Start starts a span using the hub carried by ctx: the parent span's hub
// when ctx contains a span started on one, otherwise the hub stored by
// ContextWithHub or Hub.Start. When ctx carries no hub the returned span is
// inert: it publishes nothing and all methods remain safe to call.
func Start(ctx context.Context, name string, attrs ...*Attribute) (context.Context, *Span) {
	hub := HubFromContext(ctx)
	if parent := FromContext(ctx); parent != nil && parent.hub != nil {
		hub = parent.hub
	}
	return startWithHub(ctx, hub, name, attrs...)
}

// FromContext returns the current span from ctx, or nil when absent. A nil
// span is safe to use; its methods are no-ops.
func FromContext(ctx context.Context) *Span {
	span, _ := ctx.Value(contextKey{}).(*Span)
	return span
}

// ContextWithHub returns a context carrying hub. Spans started from the
// returned context via the package-level Start use this hub when no parent
// span provides one.
func ContextWithHub(ctx context.Context, hub *Hub) context.Context {
	return context.WithValue(ctx, hubContextKey{}, hub)
}

// HubFromContext returns the hub carried by ctx, or nil when absent.
func HubFromContext(ctx context.Context) *Hub {
	hub, _ := ctx.Value(hubContextKey{}).(*Hub)
	return hub
}

// Recording reports whether the span publishes records. It is false for a
// nil or inert span and false once the span has ended, so callers can skip
// computing expensive attributes when nothing would be recorded.
func (s *Span) Recording() bool {
	if s == nil || s.hub == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.ended
}

// SpanID returns the span's ID, or "" for a nil or inert span.
func (s *Span) SpanID() string {
	if s == nil {
		return ""
	}
	return s.spanID
}

// TraceID returns the span's trace ID, or "" for a nil or inert span.
func (s *Span) TraceID() string {
	if s == nil {
		return ""
	}
	return s.traceID
}

// SetAttributes emits an update with attributes for the span.
func (s *Span) SetAttributes(attrs ...*Attribute) {
	if s == nil || s.hub == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || len(attrs) == 0 {
		return
	}
	s.hub.Publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanUpdated: sapv1.SpanUpdated_builder{
			TraceId:    new(s.traceID),
			SpanId:     new(s.spanID),
			Attributes: cloneAttributes(attrs),
		}.Build(),
	}.Build())
}

// Severity classifies an event; events without one are informational.
type Severity = sapv1.SpanEvent_Severity

const (
	SeverityInfo  Severity = sapv1.SpanEvent_SEVERITY_INFO
	SeverityWarn  Severity = sapv1.SpanEvent_SEVERITY_WARN
	SeverityError Severity = sapv1.SpanEvent_SEVERITY_ERROR
)

// EventOption configures an event emitted by [Span.AddEvent].
type EventOption func(*eventConfig)

type eventConfig struct {
	at       time.Time
	attrs    []*Attribute
	severity Severity
}

// WithTimestamp sets the event's timestamp instead of the default time.Now().
func WithTimestamp(at time.Time) EventOption {
	return func(cfg *eventConfig) {
		cfg.at = at
	}
}

// WithAttributes appends attributes to the event.
func WithAttributes(attrs ...*Attribute) EventOption {
	return func(cfg *eventConfig) {
		cfg.attrs = append(cfg.attrs, attrs...)
	}
}

// WithSeverity sets the event's severity.
func WithSeverity(severity Severity) EventOption {
	return func(cfg *eventConfig) {
		cfg.severity = severity
	}
}

// AddEvent emits a named event for the span.
func (s *Span) AddEvent(name string, opts ...EventOption) {
	if s == nil || s.hub == nil {
		return
	}
	var cfg eventConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	eventAt := cfg.at
	if eventAt.IsZero() {
		eventAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	event := sapv1.SpanEvent_builder{
		TraceId:    new(s.traceID),
		SpanId:     new(s.spanID),
		Name:       new(name),
		EventAt:    timestamppb.New(eventAt),
		Attributes: cloneAttributes(cfg.attrs),
	}
	if cfg.severity != sapv1.SpanEvent_SEVERITY_UNSPECIFIED {
		event.Severity = cfg.severity.Enum()
	}
	s.hub.Publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanEvent: event.Build(),
	}.Build())
}

// Complete marks the span as successfully completed.
func (s *Span) Complete() {
	s.end(sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE, "")
}

// Error marks the span as completed with an error.
func (s *Span) Error(err error) {
	if s == nil {
		return
	}
	if err == nil {
		s.end(sapv1.SpanEnded_TERMINAL_TYPE_ERROR, "")
		return
	}
	s.end(sapv1.SpanEnded_TERMINAL_TYPE_ERROR, err.Error())
}

func contextWithSpan(ctx context.Context, span *Span) context.Context {
	return context.WithValue(ctx, contextKey{}, span)
}

func startWithHub(ctx context.Context, hub *Hub, name string, attrs ...*Attribute) (context.Context, *Span) {
	if hub == nil {
		span := &Span{}
		return contextWithSpan(ctx, span), span
	}
	parent := FromContext(ctx)
	span := &Span{hub: hub, spanID: newID()}
	// An inert parent has no IDs to inherit; start a fresh trace instead.
	if parent != nil && parent.traceID != "" {
		span.traceID = parent.traceID
		span.parentID = parent.spanID
	} else {
		span.traceID = newID()
	}
	hub.Publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanStarted: sapv1.SpanStarted_builder{
			TraceId:      new(span.traceID),
			SpanId:       new(span.spanID),
			ParentSpanId: stringOrNil(span.parentID),
			Name:         new(name),
			StartedAt:    timestamppb.New(time.Now()),
			Attributes:   cloneAttributes(attrs),
		}.Build(),
	}.Build())
	if HubFromContext(ctx) != hub {
		ctx = ContextWithHub(ctx, hub)
	}
	return contextWithSpan(ctx, span), span
}

func (s *Span) end(terminalType sapv1.SpanEnded_TerminalType, errorMessage string) {
	if s == nil || s.hub == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.hub.Publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanEnded: sapv1.SpanEnded_builder{
			TraceId:      new(s.traceID),
			SpanId:       new(s.spanID),
			EndedAt:      timestamppb.New(time.Now()),
			TerminalType: terminalType.Enum(),
			ErrorMessage: stringOrNil(errorMessage),
		}.Build(),
	}.Build())
}

func stringOrNil(value string) *string {
	if value == "" {
		return nil
	}
	return new(value)
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
