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
	sap.String("route", "/health", "text"),
)
defer span.Complete()

span.SetAttributes(sap.String("status", "running", "badge"))
span.AddEvent("cache_miss", sap.WithAttributes(sap.String("key", "health-check")))

_, child := sap.Start(ctx, "db")
child.Complete()
```

Only the code that constructs the hub holds a `*Hub`: starting a span stores
the hub in the returned context, so downstream packages start child spans from
the context alone.

Instrumentation is safe unconditionally. A context without a hub yields inert
spans that publish nothing, and nil hubs and spans are valid no-op receivers,
so instrumented code never needs to check whether monitoring is wired up.
Spans report whether they are recording, so expensive attributes can be
skipped when nothing would observe them.

Spans always start at `time.Now()`; back-dating is deliberately unsupported,
so bracket operations with a span while they run. For moments whose timing is
only known after the fact, events accept an explicit timestamp.

```go
span.AddEvent("tls.handshake_done",
	sap.WithTimestamp(info.DoneAt),
	sap.WithAttributes(sap.Duration("took", info.Duration)),
)
```

Events carry a severity to indicate warnings and failures on a span that
otherwise keeps running; the viewers may render warn and error events
highlighted. Events without a severity are informational.

## SSE Endpoint

```go
http.Handle("/live", sapsse.NewHandler(hub))
```

The SSE payload format is protobuf JSON, one record per `data:` frame.

## Demo

Run the example live emitter/server:

```bash
go run ./example/live
```

The demo emitter periodically creates nested spans, attribute updates, point-in-time events, syntax-highlightable code attributes, recoverable warning events, and occasional terminal errors.

Viewer commands live in their own modules under `viewer/`.
