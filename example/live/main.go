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
	"github.com/endigma/sap/transport/sse"
)

func main() {
	mode := flag.String("mode", "request", "emission mode: request, nested, append")
	flag.Parse()

	if !validMode(*mode) {
		log.Fatalf("unknown mode %q (valid modes: request, nested, append)", *mode)
	}

	hub := sap.NewHub()

	go emit(context.Background(), hub, *mode)

	http.Handle("/live", sse.NewHandler(hub))
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

	rootCtx, root := hub.Start(ctx, "append cadence demo",
		sap.Attr("mode", "append", "badge"),
		sap.Attr("span_count", fmt.Sprintf("%d", spanCount), "badge"),
		sap.Attr("span_duration", spanDuration.String(), "badge"),
		sap.Attr("description", "appends child spans to this root; each child takes one second", "text"),
	)
	defer root.Complete()

	root.AddEvent("demo.started", sap.Attr("note", "each appended child span remains open for one second", "text"))
	for i := 1; i <= spanCount; i++ {
		_, span := hub.Start(rootCtx, fmt.Sprintf("appended span %02d", i),
			sap.Attr("index", fmt.Sprintf("%d", i), "badge"),
			sap.Attr("duration", spanDuration.String(), "badge"),
		)
		span.AddEvent("span.started", sap.Attr("position", fmt.Sprintf("%d/%d", i, spanCount), "badge"))

		select {
		case <-ctx.Done():
			span.AddEvent("span.canceled", sap.Attr("position", fmt.Sprintf("%d/%d", i, spanCount), "badge"))
			span.Error(ctx.Err())
			root.AddEvent("demo.canceled", sap.Attr("appended", fmt.Sprintf("%d/%d", i-1, spanCount), "badge"))
			return
		case <-time.After(spanDuration):
		}

		span.Complete()
	}
	root.AddEvent("demo.finished", sap.Attr("appended", fmt.Sprintf("%d/%d", spanCount, spanCount), "badge"))
}

func emitNestedTrace(hub *sap.Hub) {
	const (
		maxDepth = 5
		fanout   = 2
	)

	ctx, root := hub.Start(context.Background(), "nested span stress demo",
		sap.Attr("depth", fmt.Sprintf("%d", maxDepth), "badge"),
		sap.Attr("fanout", fmt.Sprintf("%d", fanout), "badge"),
		sap.Attr("shape", "intentionally noisy recursive demo", "text"),
		sap.Attr("sample_query", "select * from contrived_tree where path like 'root.%'", "code:sql"),
	)
	defer root.Complete()

	root.AddEvent("demo.started", sap.Attr("note", "creates a deliberately deep span tree", "text"))
	emitNestedChildren(ctx, hub, 1, maxDepth, fanout, "root")
	root.AddEvent("demo.finished", sap.Attr("note", "all generated children completed", "text"))
}

func emitNestedChildren(ctx context.Context, hub *sap.Hub, level, maxDepth, fanout int, path string) {
	if level > maxDepth {
		return
	}

	for i := range fanout {
		childPath := fmt.Sprintf("%s.%d", path, i+1)
		childCtx, span := hub.Start(ctx, fmt.Sprintf("nested level %d child %d", level, i+1),
			sap.Attr("level", fmt.Sprintf("%d", level), "badge"),
			sap.Attr("path", childPath, "text"),
			sap.Attr("work", fmt.Sprintf("expand synthetic node %s", childPath), "text"),
		)

		time.Sleep(time.Duration(20+rand.IntN(40)) * time.Millisecond)
		span.SetAttributes(sap.Attr("state", "expanding", "badge"))
		span.AddEvent("node.expanded", sap.Attr("children", fmt.Sprintf("%d", fanout), "badge"))

		emitNestedChildren(childCtx, hub, level+1, maxDepth, fanout, childPath)

		if rand.IntN(12) == 0 {
			span.AddEvent("contrived.warning", sap.Attr("message", "synthetic nested-span warning", "text"))
		}
		time.Sleep(time.Duration(10+rand.IntN(20)) * time.Millisecond)
		span.Complete()
	}
}

func emitTrace(hub *sap.Hub) {
	ctx, root := hub.Start(context.Background(), "serve request",
		sap.LabeledAttr("method", "Method", "GET", "badge"),
		sap.LabeledAttr("route", "Route", "/projects/:id", "badge"),
		sap.LabeledAttr("query", "Query", "select * from projects where id = $1", "code:sql"),
	)
	defer root.Complete()

	root.AddEvent("request.received", sap.Attr("remote_addr", "127.0.0.1"), sap.Attr("cache", "miss", "badge"))
	time.Sleep(1200 * time.Millisecond)
	root.SetAttributes(
		sap.Attr("status", "authorizing", "badge"),
		sap.Attr("input", "{\n  \"project_id\": 42,\n  \"expand\": [\"owner\", \"members\"]\n}", "code:json"),
	)

	_, loadUser := hub.Start(ctx, "load user", sap.Attr("query", "select id, role from users where id = $1", "code:sql"))
	time.Sleep(1600 * time.Millisecond)
	loadUser.AddEvent("db.row", sap.Attr("user_id", "7"), sap.Attr("role", "admin", "badge"))
	time.Sleep(900 * time.Millisecond)
	loadUser.Complete()

	_, assemble := hub.Start(ctx, "assemble response", sap.Attr("template", "project_detail", "badge"))
	assemble.AddEvent("renderer.partial", sap.Attr("name", "header"))
	time.Sleep(1400 * time.Millisecond)
	if rand.IntN(4) == 0 {
		assemble.AddEvent("warning", sap.Attr("detail", "owner profile missing avatar", "text"))
	}
	assemble.SetAttributes(sap.Attr("payload_preview", "{\n  \"id\": 42,\n  \"name\": \"Sap\",\n  \"owner\": {\n    \"id\": 7\n  }\n}", "code:json"))
	time.Sleep(1800 * time.Millisecond)
	assemble.Complete()

	time.Sleep(1200 * time.Millisecond)
	if rand.IntN(5) == 0 {
		root.AddEvent("recoverable.error", sap.Attr("message", "cache backend timeout", "text"))
	}
	if rand.IntN(6) == 0 {
		root.Error(errors.New("upstream dependency failed"))
		return
	}
	_ = ctx
}
