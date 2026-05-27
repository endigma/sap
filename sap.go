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

// Span tracks a timed unit of work and emits lifecycle records.
type Span struct {
	mu       sync.Mutex
	hub      *Hub
	traceID  string
	spanID   string
	parentID string
	ended    bool
}

// Start starts a span on the hub and returns a context containing it.
func (h *Hub) Start(ctx context.Context, name string, attrs ...*Attribute) (context.Context, *Span) {
	return startWithHub(ctx, h, name, attrs...)
}

// FromContext returns the current span from ctx, if one is present.
func FromContext(ctx context.Context) *Span {
	span, _ := ctx.Value(contextKey{}).(*Span)
	return span
}

// SetAttributes emits an update with attributes for the span.
func (s *Span) SetAttributes(attrs ...*Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended || len(attrs) == 0 {
		return
	}
	s.publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanUpdated: sapv1.SpanUpdated_builder{
			TraceId:    new(s.traceID),
			SpanId:     new(s.spanID),
			Attributes: cloneAttributes(attrs),
		}.Build(),
	}.Build())
}

// AddEvent emits a named event for the span.
func (s *Span) AddEvent(name string, attrs ...*Attribute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanEvent: sapv1.SpanEvent_builder{
			TraceId:    new(s.traceID),
			SpanId:     new(s.spanID),
			Name:       new(name),
			EventAt:    timestamppb.Now(),
			Attributes: cloneAttributes(attrs),
		}.Build(),
	}.Build())
}

// Complete marks the span as successfully completed.
func (s *Span) Complete() {
	s.end(sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE, "")
}

// Error marks the span as completed with an error.
func (s *Span) Error(err error) {
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
	parent := FromContext(ctx)
	span := &Span{hub: hub, spanID: newID()}
	if parent != nil {
		span.traceID = parent.traceID
		span.parentID = parent.spanID
	} else {
		span.traceID = newID()
	}
	span.publish(sapv1.Record_builder{
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
	return contextWithSpan(ctx, span), span
}

func (s *Span) end(terminalType sapv1.SpanEnded_TerminalType, errorMessage string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return
	}
	s.ended = true
	s.publish(sapv1.Record_builder{
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

func (s *Span) publish(record *sapv1.Record) {
	if s.hub == nil {
		return
	}
	s.hub.Publish(record)
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
