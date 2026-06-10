# sap

Like tracing, but stupid and pretty.

`sap` is a span-first live monitoring library for Go. Applications emit canonical protobuf records for span starts, updates, ends, and span-attached events. Live consumers can subscribe through SSE, and viewers can reconstruct current state through `state`.

## Packages

- `github.com/endigma/sap`: root SDK and in-process record hub
- `github.com/endigma/sap/gen/sap/v1`: generated protobuf record types
- `github.com/endigma/sap/transport/sapsse`: SSE server/client transport using protobuf JSON
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

span.SetAttributes(sap.String("status", "running", "badge"))
span.AddEvent("cache_miss", sap.WithAttributes(sap.String("key", "health-check")))

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

Spans always start at `time.Now()`; back-dating is deliberately unsupported,
so bracket operations with a span while they run. For events where duration is
only known after completion, use `span.AddEvent(...)`

```go
span.AddEvent("tls.handshake_done",
	sap.WithTimestamp(info.DoneAt),
	sap.WithAttributes(sap.Duration("took", info.Duration)),
)
```

Events carry a severity to indicate warnings and failures on a span
that otherwise keeps running: `sap.WithSeverity(sap.SeverityWarn)` or
`sap.SeverityError` which the viewers may render highlighted. Events without
severity or set to `sap.SeverityInfo` are informational.

Attribute values are strings; `sap.String` creates one directly, and
`sap.Int`, `sap.Int64`, `sap.Float64`, `sap.Bool`, and `sap.Duration`
format common types. `span.SpanID()` and
`span.TraceID()` expose the span's identifiers for correlating with other
tracing or logging systems.

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
