package sapsse

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/endigma/sap"
	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHandlerStreamsLiveRecords(t *testing.T) {
	hub := sap.NewHub()
	defer hub.Close()

	srv := httptest.NewServer(NewHandler(hub))
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	}()

	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	time.Sleep(20 * time.Millisecond)
	hub.Publish(sapv1.Record_builder{
		EmittedAt: timestamppb.Now(),
		SpanStarted: sapv1.SpanStarted_builder{
			TraceId: new("trace-1"),
			SpanId:  new("span-1"),
			Name:    new("root"),
		}.Build(),
	}.Build())

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var got sapv1.Record
		if err := protojson.Unmarshal([]byte(line[len("data: "):]), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		started := got.GetSpanStarted()
		if started == nil || started.GetTraceId() != "trace-1" || started.GetName() != "root" {
			t.Fatalf("record = %+v", started)
		}
		return
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stream: %v", err)
	}
	t.Fatal("stream ended before a data frame arrived")
}

func TestStream(t *testing.T) {
	t.Run("delivers a severity event through the full round trip", func(t *testing.T) {
		hub := sap.NewHub()
		defer hub.Close()

		srv := httptest.NewServer(NewHandler(hub))
		defer srv.Close()

		// Cancel the client before srv.Close so the handler's streaming
		// request ends and Close can return.
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		records, states := Stream(ctx, srv.URL)
		waitForConnected(t, states)

		hub.Publish(sapv1.Record_builder{
			EmittedAt: timestamppb.Now(),
			SpanEvent: sapv1.SpanEvent_builder{
				TraceId:  new("trace-1"),
				SpanId:   new("span-1"),
				Name:     new("recoverable.error"),
				EventAt:  timestamppb.Now(),
				Severity: sapv1.SpanEvent_SEVERITY_ERROR.Enum(),
			}.Build(),
		}.Build())

		select {
		case msg := <-records:
			event := msg.Record.GetSpanEvent()
			if event == nil || event.GetName() != "recoverable.error" {
				t.Fatalf("record = %+v", msg.Record)
			}
			if event.GetSeverity() != sapv1.SpanEvent_SEVERITY_ERROR {
				t.Fatalf("severity = %v, want SEVERITY_ERROR", event.GetSeverity())
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no record arrived")
		}
	})
}

func waitForConnected(t *testing.T, states <-chan ConnectionState) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case state := <-states:
			if state.State == StateConnected {
				return
			}
		case <-deadline:
			t.Fatal("client never connected")
		}
	}
}
