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
