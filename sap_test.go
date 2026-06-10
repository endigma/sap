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

	ctx, root := hub.Start(context.Background(), "root", LabeledAttr("goal", "Goal", "ship it", "text", "text"))
	_, child := hub.Start(ctx, "child", Attr("step", "one"))
	root.SetAttributes(Attr("status", "running", "badge"))
	root.AddEvent("warn", Attr("message", "careful", "text"))
	root.Complete()
	root.Error(errors.New("ignored"))
	root.SetAttributes(Attr("late", "ignored"))
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
		span.SetAttributes(Attr("k", "v"))
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
		hub.Publish(nil)
	})

	t.Run("subscribe returns a closed channel", func(t *testing.T) {
		ch, cancel := hub.Subscribe(4)
		defer cancel()
		if _, ok := <-ch; ok {
			t.Fatal("channel from nil hub is not closed")
		}
	})

	t.Run("close is a no-op", func(t *testing.T) {
		hub.Close()
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
		span.SetAttributes(Attr("k", "v"))
		span.AddEvent("event")
		span.Complete()
		span.Error(errors.New("boom"))
	})
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
