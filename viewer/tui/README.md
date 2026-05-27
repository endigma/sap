# sap TUI viewer

Bubble Tea terminal viewer for a sap SSE stream.

## Run

Start the example emitter/server from the repository root:

```bash
go run ./example/live
```

In another terminal, run the TUI viewer:

```bash
cd viewer/tui
go run .
```

By default, the viewer hides spans whose parent was not seen by the viewer. To show those spans as roots instead, add `-allow-unknown-parents`.

```bash
go run . -allow-unknown-parents
```
