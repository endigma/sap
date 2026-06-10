// Package main runs the live Sap SSE example.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/endigma/sap"
	"github.com/endigma/sap/transport/sapsse"
)

func main() {
	mode := flag.String("mode", "request", "emission mode: request, nested, append")
	flag.Parse()

	if !validMode(*mode) {
		log.Fatalf("unknown mode %q (valid modes: request, nested, append)", *mode)
	}

	hub := sap.NewHub()

	go emit(context.Background(), hub, *mode)

	http.Handle("/live", sapsse.NewHandler(hub))
	log.Printf("live monitor on http://127.0.0.1:8080/live (mode=%s)", *mode)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func validMode(mode string) bool {
	switch mode {
	case "request", "nested", "append":
		return true
	default:
		return false
	}
}

func emit(ctx context.Context, hub *sap.Hub, mode string) {
	ticker := time.NewTicker(8 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			emitMode(ctx, hub, mode)
		}
	}
}

func emitMode(ctx context.Context, hub *sap.Hub, mode string) {
	switch mode {
	case "request":
		emitTrace(hub)
	case "nested":
		emitNestedTrace(hub)
	case "append":
		emitAppendTrace(ctx, hub)
	}
}

func emitAppendTrace(ctx context.Context, hub *sap.Hub) {
	const (
		spanCount    = 20
		spanDuration = time.Second
	)

	rootCtx, root := hub.Start(
		ctx, "append cadence demo",
		sap.String("mode", "append", "badge"),
		sap.Int("span_count", spanCount, "badge"),
		sap.Duration("span_duration", spanDuration, "badge"),
		sap.String("description", "appends child spans to this root; each child takes one second", "text"),
	)
	defer root.Complete()

	root.AddEvent("demo.started", sap.WithAttributes(sap.String("note", "each appended child span remains open for one second", "text")))
	for i := 1; i <= spanCount; i++ {
		_, span := hub.Start(
			rootCtx, fmt.Sprintf("appended span %02d", i),
			sap.Int("index", i, "badge"),
			sap.Duration("duration", spanDuration, "badge"),
		)
		span.AddEvent("span.started", sap.WithAttributes(sap.String("position", fmt.Sprintf("%d/%d", i, spanCount), "badge")))

		select {
		case <-ctx.Done():
			span.AddEvent("span.canceled", sap.WithSeverity(sap.SeverityWarn), sap.WithAttributes(sap.String("position", fmt.Sprintf("%d/%d", i, spanCount), "badge")))
			span.Error(ctx.Err())
			root.AddEvent("demo.canceled", sap.WithSeverity(sap.SeverityWarn), sap.WithAttributes(sap.String("appended", fmt.Sprintf("%d/%d", i-1, spanCount), "badge")))
			return
		case <-time.After(spanDuration):
		}

		span.Complete()
	}
	root.AddEvent("demo.finished", sap.WithAttributes(sap.String("appended", fmt.Sprintf("%d/%d", spanCount, spanCount), "badge")))
}

func emitNestedTrace(hub *sap.Hub) {
	const (
		maxDepth = 5
		fanout   = 2
	)

	ctx, root := hub.Start(
		context.Background(), "nested span stress demo",
		sap.Int("depth", maxDepth, "badge"),
		sap.Int("fanout", fanout, "badge"),
		sap.String("shape", "intentionally noisy recursive demo", "text"),
		sap.String("sample_query", "select * from contrived_tree where path like 'root.%'", "code:sql"),
	)
	defer root.Complete()

	root.AddEvent("demo.started", sap.WithAttributes(sap.String("note", "creates a deliberately deep span tree", "text")))
	emitNestedChildren(ctx, hub, 1, maxDepth, fanout, "root")
	root.AddEvent("demo.finished", sap.WithAttributes(sap.String("note", "all generated children completed", "text")))
}

func emitNestedChildren(ctx context.Context, hub *sap.Hub, level, maxDepth, fanout int, path string) {
	if level > maxDepth {
		return
	}

	for i := range fanout {
		childPath := fmt.Sprintf("%s.%d", path, i+1)
		childCtx, span := hub.Start(
			ctx, fmt.Sprintf("nested level %d child %d", level, i+1),
			sap.Int("level", level, "badge"),
			sap.String("path", childPath, "text"),
			sap.String("work", fmt.Sprintf("expand synthetic node %s", childPath), "text"),
		)

		time.Sleep(time.Duration(20+rand.IntN(40)) * time.Millisecond)
		span.SetAttributes(sap.String("state", "expanding", "badge"))
		span.AddEvent("node.expanded", sap.WithAttributes(sap.Int("children", fanout, "badge")))

		emitNestedChildren(childCtx, hub, level+1, maxDepth, fanout, childPath)

		if rand.IntN(12) == 0 {
			span.AddEvent("contrived.warning", sap.WithSeverity(sap.SeverityWarn), sap.WithAttributes(sap.String("message", "synthetic nested-span warning", "text")))
		}
		time.Sleep(time.Duration(10+rand.IntN(20)) * time.Millisecond)
		span.Complete()
	}
}

// requestTraceFails alternates request-mode traces between error and
// success endings.
var requestTraceFails bool

func emitTrace(hub *sap.Hub) {
	fail := requestTraceFails
	requestTraceFails = !requestTraceFails

	ctx, root := hub.Start(
		context.Background(), "serve request",
		sap.LabeledAttr("method", "Method", "GET", "badge"),
		sap.LabeledAttr("route", "Route", "/projects/:id", "badge"),
		sap.LabeledAttr("query", "Query", "select * from projects where id = $1", "code:sql"),
	)
	defer root.Complete()

	root.AddEvent("request.received", sap.WithAttributes(sap.String("remote_addr", "127.0.0.1"), sap.String("cache", "miss", "badge")))
	time.Sleep(1200 * time.Millisecond)
	root.SetAttributes(
		sap.String("status", "authorizing", "badge"),
		sap.String("input", "{\n  \"project_id\": 42,\n  \"expand\": [\"owner\", \"members\"]\n}", "code:json"),
	)

	_, loadUser := hub.Start(ctx, "load user", sap.String("query", "select id, role from users where id = $1", "code:sql"))
	time.Sleep(1600 * time.Millisecond)
	loadUser.AddEvent("db.row", sap.WithAttributes(sap.String("user_id", "7"), sap.String("role", "admin", "badge")))
	time.Sleep(900 * time.Millisecond)
	loadUser.Complete()

	_, assemble := hub.Start(ctx, "assemble response", sap.String("template", "project_detail", "badge"))
	assemble.AddEvent("renderer.partial", sap.WithAttributes(sap.String("name", "header")))
	time.Sleep(1400 * time.Millisecond)
	assemble.AddEvent("warning", sap.WithSeverity(sap.SeverityWarn), sap.WithAttributes(sap.String("detail", "owner profile missing avatar", "text")))
	assemble.SetAttributes(sap.String("payload_preview", "{\n  \"id\": 42,\n  \"name\": \"Sap\",\n  \"owner\": {\n    \"id\": 7\n  }\n}", "code:json"))
	time.Sleep(1800 * time.Millisecond)
	assemble.Complete()

	time.Sleep(1200 * time.Millisecond)
	root.AddEvent("recoverable.error", sap.WithSeverity(sap.SeverityError), sap.WithAttributes(sap.String("message", "cache backend timeout", "text")))
	if fail {
		root.Error(errors.New("upstream dependency failed"))
	}
}
