package main

import (
	"bytes"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/exp/teatest"
	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sapsse"
	"github.com/muesli/termenv"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii)
	os.Exit(m.Run())
}

func TestModel(t *testing.T) {
	t.Run("shows a trailing severity event after the repaint tick", func(t *testing.T) {
		m := newModel(&atomic.Bool{}, state.StoreOptions{})
		m.Update(tea.WindowSizeMsg{Width: 100, Height: 200})

		now := time.Now()
		base := now.Add(-9 * time.Second)
		records := []*sapv1.Record{
			spanStarted("root", "", "serve request", base),
			spanStarted("load-user", "root", "load user", base.Add(1200*time.Millisecond)),
			spanEnded("load-user", base.Add(3700*time.Millisecond)),
			spanEvent("root", "recoverable.error", sapv1.SpanEvent_SEVERITY_ERROR, base.Add(8100*time.Millisecond)),
			spanEnded("root", base.Add(8100*time.Millisecond)),
		}
		for _, record := range records {
			m.Update(eventMsg(sapsse.RecordMessage{Record: record}))
		}
		m.Update(repaintMsg{})

		view := m.vp.View()
		if !strings.Contains(view, "recoverable.error") {
			t.Fatalf("viewport does not contain the event:\n%s", view)
		}
	})
}

func TestProgram(t *testing.T) {
	t.Run("renders a completed request trace", func(t *testing.T) {
		m := newModel(&atomic.Bool{}, state.StoreOptions{})
		tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

		base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		records := []*sapv1.Record{
			spanStarted("root", "", "serve request", base),
			spanEvent("root", "request.received", sapv1.SpanEvent_SEVERITY_UNSPECIFIED, base.Add(100*time.Millisecond)),
			spanStarted("load-user", "root", "load user", base.Add(1200*time.Millisecond)),
			spanEnded("load-user", base.Add(3700*time.Millisecond)),
			spanStarted("assemble", "root", "assemble response", base.Add(3700*time.Millisecond)),
			spanEvent("assemble", "warning", sapv1.SpanEvent_SEVERITY_WARN, base.Add(5100*time.Millisecond)),
			spanEnded("assemble", base.Add(6900*time.Millisecond)),
			spanEvent("root", "recoverable.error", sapv1.SpanEvent_SEVERITY_ERROR, base.Add(8100*time.Millisecond)),
			spanEnded("root", base.Add(8100*time.Millisecond)),
		}
		for _, record := range records {
			tm.Send(eventMsg(sapsse.RecordMessage{Record: record}))
		}

		teatest.WaitFor(t, tm.Output(), func(bts []byte) bool {
			return bytes.Contains(bts, []byte("recoverable.error"))
		}, teatest.WithDuration(3*time.Second))

		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

		// Golden the final model view rather than the raw output stream:
		// repaints are debounced, so the frame count is timing-dependent.
		final, ok := tm.FinalModel(t).(*model)
		if !ok {
			t.Fatalf("final model has type %T", tm.FinalModel(t))
		}
		teatest.RequireEqualOutput(t, []byte(final.View()))
	})
}

func spanStarted(spanID, parentID, name string, at time.Time) *sapv1.Record {
	builder := sapv1.SpanStarted_builder{
		TraceId:   new("trace-1"),
		SpanId:    new(spanID),
		Name:      new(name),
		StartedAt: timestamppb.New(at),
	}
	if parentID != "" {
		builder.ParentSpanId = new(parentID)
	}
	return sapv1.Record_builder{SpanStarted: builder.Build()}.Build()
}

func spanEnded(spanID string, at time.Time) *sapv1.Record {
	return sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{
		TraceId:      new("trace-1"),
		SpanId:       new(spanID),
		EndedAt:      timestamppb.New(at),
		TerminalType: sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE.Enum(),
	}.Build()}.Build()
}

func spanEvent(spanID, name string, severity sapv1.SpanEvent_Severity, at time.Time) *sapv1.Record {
	return sapv1.Record_builder{SpanEvent: sapv1.SpanEvent_builder{
		TraceId:  new("trace-1"),
		SpanId:   new(spanID),
		Name:     new(name),
		EventAt:  timestamppb.New(at),
		Severity: severity.Enum(),
	}.Build()}.Build()
}
