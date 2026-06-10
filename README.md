# sap

Like tracing, but stupid and pretty.

`sap` is a span-first live monitoring library for Go. Applications emit canonical protobuf records for span starts, updates, ends, and span-attached events. Live consumers can subscribe through SSE, and viewers can reconstruct current state through `state`.

## Packages

- `github.com/endigma/sap`: root SDK and in-process record hub
- `github.com/endigma/sap/gen/sap/v1`: generated protobuf record types
- `github.com/endigma/sap/transport/sse`: SSE server/client transport using protobuf JSON
- `github.com/endigma/sap/state`: shared record-to-state reconstruction

## Generate Code

```bash
mise run proto
```

## Basic Usage

```go
hub := sap.NewHub()

ctx, span := hub.Start(context.Background(), "request",
	 sap.LabeledAttr("route", "Route", "/health", "text"),
)
defer span.Complete()

span.SetAttributes(sap.Attr("status", "running", "badge"))
span.AddEvent("cache_miss", sap.Attr("key", "health-check"))

_, child := sap.Start(ctx, "db")
child.Complete()
```

Only the code that constructs the hub needs a `*Hub`: `hub.Start` stores the
hub in the returned context, so downstream packages start child spans with
`sap.Start(ctx, ...)`. A context without a span can carry a hub via
`sap.ContextWithHub`.

Instrumentation is safe unconditionally. When the context carries no hub,
`sap.Start` returns an inert span that publishes nothing, and all methods on
a nil `*Hub` or nil `*Span` (as returned by `sap.FromContext` when absent)
are no-ops. When attributes are expensive to compute, gate them with
`span.Recording()`, which is false for nil, inert, and ended spans.

## SSE Endpoint

```go
http.Handle("/live", sse.NewHandler(hub))
```

The SSE payload format is protobuf JSON, one record per `data:` frame.

## Demo

Run the example live emitter/server:

```bash
go run ./example/live
```

The demo emitter periodically creates nested spans, attribute updates, point-in-time events, syntax-highlightable code attributes, recoverable warning events, and occasional terminal errors.

Viewer commands live in their own modules under `viewer/`.
