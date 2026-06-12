package sap

import (
	"context"
	"errors"
	"testing"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
)

func TestSpanLifecycle(t *testing.T) {
	hub := NewHub()
	t.Cleanup(hub.Close)

	records, cancel := hub.Subscribe(32)
	defer cancel()

	ctx, root := hub.Start(context.Background(), "root", String("goal", "ship it", "text"))
	_, child := hub.Start(ctx, "child", String("step", "one"))
	root.SetAttributes(String("status", "running", "badge"))
	root.AddEvent("warn", WithAttributes(String("message", "careful", "text")))
	root.Complete()
	root.Error(errors.New("ignored"))
	root.SetAttributes(String("late", "ignored"))
	root.AddEvent("late")
	child.Error(errors.New("boom"))

	got := drainRecords(records, 6, time.Second)
	if len(got) != 6 {
		t.Fatalf("got %d records, want 6", len(got))
	}

	started := got[0].GetSpanStarted()
	if started == nil || started.GetName() != "root" {
		t.Fatalf("record[0] kind = %v", got[0].WhichKind())
	}
	if started.GetTraceId() == "" || started.GetSpanId() == "" {
		t.Fatalf("root ids missing: %+v", started)
	}
	if len(started.GetAttributes()) != 1 || started.GetAttributes()[0].GetKey() != "goal" || len(started.GetAttributes()[0].GetDisplayHints()) != 1 {
		t.Fatalf("root start attrs = %+v", started.GetAttributes())
	}

	childStarted := got[1].GetSpanStarted()
	if childStarted == nil || childStarted.GetParentSpanId() != started.GetSpanId() || childStarted.GetTraceId() != started.GetTraceId() {
		t.Fatalf("child start = %+v", childStarted)
	}

	updated := got[2].GetSpanUpdated()
	if updated == nil || updated.GetSpanId() != started.GetSpanId() || len(updated.GetAttributes()) != 1 || updated.GetAttributes()[0].GetKey() != "status" {
		t.Fatalf("update = %+v", updated)
	}

	event := got[3].GetSpanEvent()
	if event == nil || event.GetName() != "warn" || event.GetSpanId() != started.GetSpanId() {
		t.Fatalf("event = %+v", event)
	}

	ended := got[4].GetSpanEnded()
	if ended == nil || ended.GetSpanId() != started.GetSpanId() || ended.GetTerminalType() != sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE {
		t.Fatalf("root end = %+v", ended)
	}

	childEnded := got[5].GetSpanEnded()
	if childEnded == nil || childEnded.GetSpanId() != childStarted.GetSpanId() || childEnded.GetTerminalType() != sapv1.SpanEnded_TERMINAL_TYPE_ERROR || childEnded.GetErrorMessage() != "boom" {
		t.Fatalf("child end = %+v", childEnded)
	}

	if gotSpan := FromContext(ctx); gotSpan != root {
		t.Fatalf("FromContext(ctx) mismatch")
	}
}

func TestHubCloseClosesSubscribers(t *testing.T) {
	hub := NewHub()
	ch, cancel := hub.Subscribe(1)
	defer cancel()
	hub.Close()
	_, ok := <-ch
	if ok {
		t.Fatal("subscriber channel still open after Close")
	}
}

func TestStart(t *testing.T) {
	t.Run("uses the parent span's hub from the context", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		records, cancel := hub.Subscribe(8)
		defer cancel()

		ctx, root := hub.Start(context.Background(), "root")
		childCtx, child := Start(ctx, "child")
		child.Complete()
		root.Complete()

		got := drainRecords(records, 4, time.Second)
		if len(got) != 4 {
			t.Fatalf("got %d records, want 4", len(got))
		}
		rootStarted := got[0].GetSpanStarted()
		childStarted := got[1].GetSpanStarted()
		if childStarted == nil || childStarted.GetName() != "child" {
			t.Fatalf("record[1] = %+v", got[1])
		}
		if childStarted.GetParentSpanId() != rootStarted.GetSpanId() || childStarted.GetTraceId() != rootStarted.GetTraceId() {
			t.Fatalf("child not parented to root: %+v", childStarted)
		}
		if FromContext(childCtx) != child {
			t.Fatal("child context does not carry the child span")
		}
	})

	t.Run("uses the hub stored in context when no span is present", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		records, cancel := hub.Subscribe(8)
		defer cancel()

		ctx := ContextWithHub(context.Background(), hub)
		_, span := Start(ctx, "root")
		span.Complete()

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		started := got[0].GetSpanStarted()
		if started == nil || started.GetName() != "root" || started.GetParentSpanId() != "" {
			t.Fatalf("record[0] = %+v", got[0])
		}
	})

	t.Run("starts a fresh trace under an inert parent span", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		records, cancel := hub.Subscribe(8)
		defer cancel()

		_, inert := Start(context.Background(), "inert")
		ctx := ContextWithHub(context.Background(), hub)
		ctx = contextWithSpan(ctx, inert)
		_, span := Start(ctx, "child")
		span.Complete()

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 || got[0].GetSpanStarted().GetName() != "child" {
			t.Fatalf("got %d records, want child start and end", len(got))
		}
		started := got[0].GetSpanStarted()
		if started.GetTraceId() == "" || started.GetParentSpanId() != "" {
			t.Fatalf("child should root a fresh trace, got %+v", started)
		}
	})

	t.Run("returns an inert span when context carries no hub", func(t *testing.T) {
		ctx, span := Start(context.Background(), "orphan")
		if span == nil {
			t.Fatal("Start returned a nil span")
		}
		span.SetAttributes(String("k", "v"))
		span.AddEvent("event")
		span.Complete()
		if FromContext(ctx) != span {
			t.Fatal("context does not carry the span")
		}
		if HubFromContext(ctx) != nil {
			t.Fatal("context unexpectedly carries a hub")
		}
	})
}

func TestHubStart(t *testing.T) {
	t.Run("stores the hub in the returned context", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		ctx, span := hub.Start(context.Background(), "root")
		defer span.Complete()
		if HubFromContext(ctx) != hub {
			t.Fatal("context does not carry the hub")
		}
	})
}

func TestSpanRecording(t *testing.T) {
	t.Run("is true for a live span", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		_, span := hub.Start(context.Background(), "op")
		defer span.Complete()
		if !span.Recording() {
			t.Fatal("live span is not recording")
		}
	})

	t.Run("is false after the span ends", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		_, span := hub.Start(context.Background(), "op")
		span.Complete()
		if span.Recording() {
			t.Fatal("ended span is still recording")
		}
	})

	t.Run("is false for an inert span", func(t *testing.T) {
		_, span := Start(context.Background(), "op")
		if span.Recording() {
			t.Fatal("inert span reports recording")
		}
	})

	t.Run("is false for a nil span", func(t *testing.T) {
		var span *Span
		if span.Recording() {
			t.Fatal("nil span reports recording")
		}
	})
}

func TestNilHub(t *testing.T) {
	var hub *Hub

	t.Run("start returns a usable inert span", func(t *testing.T) {
		ctx, span := hub.Start(context.Background(), "op")
		span.AddEvent("event")
		span.Complete()
		if FromContext(ctx) != span {
			t.Fatal("context does not carry the span")
		}
		if HubFromContext(ctx) != nil {
			t.Fatal("nil hub was stored in context")
		}
	})

	t.Run("publish is a no-op", func(t *testing.T) {
		mustReturn(t, func() { hub.Publish(nil) })
	})

	t.Run("subscribe returns a closed channel", func(t *testing.T) {
		ch, cancel := hub.Subscribe(4)
		defer cancel()
		if _, ok := <-ch; ok {
			t.Fatal("channel from nil hub is not closed")
		}
	})

	t.Run("close is a no-op", func(t *testing.T) {
		mustReturn(t, func() { hub.Close() })
	})
}

func TestNilSpan(t *testing.T) {
	t.Run("is returned by FromContext on an empty context", func(t *testing.T) {
		if span := FromContext(context.Background()); span != nil {
			t.Fatalf("FromContext = %+v, want nil", span)
		}
	})

	t.Run("ignores all method calls", func(t *testing.T) {
		var span *Span
		mustReturn(t, func() {
			span.SetAttributes(String("k", "v"))
			span.AddEvent("event")
			span.Complete()
			span.Error(errors.New("boom"))
		})
	})
}

func TestAddEvent(t *testing.T) {
	newSpan := func(t *testing.T) (*Span, <-chan *sapv1.Record) {
		t.Helper()
		hub := NewHub()
		t.Cleanup(hub.Close)
		records, cancel := hub.Subscribe(8)
		t.Cleanup(cancel)
		_, span := hub.Start(context.Background(), "op")
		return span, records
	}

	t.Run("stamps the event with the provided timestamp", func(t *testing.T) {
		span, records := newSpan(t)
		at := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		span.AddEvent("late.fact", WithTimestamp(at))

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		event := got[1].GetSpanEvent()
		if event == nil || !event.GetEventAt().AsTime().Equal(at) {
			t.Fatalf("event = %+v, want event_at %v", event, at)
		}
	})

	t.Run("defaults the event timestamp to now", func(t *testing.T) {
		span, records := newSpan(t)
		before := time.Now()
		span.AddEvent("event")
		after := time.Now()

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		eventAt := got[1].GetSpanEvent().GetEventAt().AsTime()
		if eventAt.Before(before) || eventAt.After(after) {
			t.Fatalf("event_at = %v, want within [%v, %v]", eventAt, before, after)
		}
	})

	t.Run("records the provided severity", func(t *testing.T) {
		span, records := newSpan(t)
		span.AddEvent("cache.write_failed", WithSeverity(SeverityError))

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		if severity := got[1].GetSpanEvent().GetSeverity(); severity != sapv1.SpanEvent_SEVERITY_ERROR {
			t.Fatalf("severity = %v, want SEVERITY_ERROR", severity)
		}
	})

	t.Run("defaults to info severity when none is provided", func(t *testing.T) {
		span, records := newSpan(t)
		span.AddEvent("event")

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		event := got[1].GetSpanEvent()
		if event.HasSeverity() {
			t.Fatalf("severity = %v, want unset on the wire", event.GetSeverity())
		}
		if severity := event.GetSeverity(); severity != sapv1.SpanEvent_SEVERITY_INFO {
			t.Fatalf("severity = %v, want default SEVERITY_INFO", severity)
		}
	})

	t.Run("accumulates attributes across options and skips nil options", func(t *testing.T) {
		span, records := newSpan(t)
		span.AddEvent(
			"event",
			WithAttributes(String("first", "1")),
			nil,
			WithAttributes(String("second", "2"), String("third", "3")),
		)

		got := drainRecords(records, 2, time.Second)
		if len(got) != 2 {
			t.Fatalf("got %d records, want 2", len(got))
		}
		attrs := got[1].GetSpanEvent().GetAttributes()
		if len(attrs) != 3 || attrs[0].GetKey() != "first" || attrs[1].GetKey() != "second" || attrs[2].GetKey() != "third" {
			t.Fatalf("event attrs = %+v", attrs)
		}
	})
}

func TestSpanIdentifiers(t *testing.T) {
	t.Run("match the published start record", func(t *testing.T) {
		hub := NewHub()
		t.Cleanup(hub.Close)
		records, cancel := hub.Subscribe(8)
		defer cancel()

		_, span := hub.Start(context.Background(), "op")
		defer span.Complete()

		got := drainRecords(records, 1, time.Second)
		if len(got) != 1 {
			t.Fatalf("got %d records, want 1", len(got))
		}
		started := got[0].GetSpanStarted()
		if span.SpanID() == "" || span.SpanID() != started.GetSpanId() {
			t.Fatalf("SpanID() = %q, record has %q", span.SpanID(), started.GetSpanId())
		}
		if span.TraceID() == "" || span.TraceID() != started.GetTraceId() {
			t.Fatalf("TraceID() = %q, record has %q", span.TraceID(), started.GetTraceId())
		}
	})

	t.Run("are empty for an inert span", func(t *testing.T) {
		_, span := Start(context.Background(), "orphan")
		if span.SpanID() != "" || span.TraceID() != "" {
			t.Fatalf("inert span IDs = %q/%q, want empty", span.SpanID(), span.TraceID())
		}
	})

	t.Run("are empty for a nil span", func(t *testing.T) {
		var span *Span
		if span.SpanID() != "" || span.TraceID() != "" {
			t.Fatal("nil span returned non-empty IDs")
		}
	})
}

func TestTypedAttributeHelpers(t *testing.T) {
	t.Run("format values as strings", func(t *testing.T) {
		cases := []struct {
			attr *Attribute
			key  string
			want string
		}{
			{Bool("ok", true), "ok", "true"},
			{Int("count", -3), "count", "-3"},
			{Int64("big", 1<<40), "big", "1099511627776"},
			{Float64("ratio", 0.25), "ratio", "0.25"},
			{Duration("took", 1500*time.Millisecond), "took", "1.5s"},
		}
		for _, tc := range cases {
			if tc.attr.GetKey() != tc.key || tc.attr.GetValue() != tc.want {
				t.Errorf("attr %q = %q, want %q", tc.attr.GetKey(), tc.attr.GetValue(), tc.want)
			}
		}
	})

	t.Run("pass display hints through", func(t *testing.T) {
		attr := Int("count", 7, "badge")
		if len(attr.GetDisplayHints()) != 1 || attr.GetDisplayHints()[0] != "badge" {
			t.Fatalf("display hints = %+v, want [badge]", attr.GetDisplayHints())
		}
	})
}

// mustReturn fails the test when fn panics or does not return within a
// second, instead of crashing the test binary or hanging silently.
func mustReturn(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan any, 1)
	go func() {
		defer func() { done <- recover() }()
		fn()
	}()
	select {
	case r := <-done:
		if r != nil {
			t.Fatalf("panicked: %v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("did not return")
	}
}

func drainRecords(ch <-chan *sapv1.Record, want int, timeout time.Duration) []*sapv1.Record {
	deadline := time.After(timeout)
	got := make([]*sapv1.Record, 0, want)
	for len(got) < want {
		select {
		case record := <-ch:
			if record != nil {
				got = append(got, record)
			}
		case <-deadline:
			return got
		}
	}
	return got
}
