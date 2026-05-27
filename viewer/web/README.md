# sap web viewer

HTMX/AlpineJS web viewer for a sap SSE stream.

## Run

Start the example emitter/server from the repository root:

```bash
go run ./example/live
```

In another terminal, run the web viewer:

```bash
cd viewer/web
go run .
```

Then open <http://127.0.0.1:8090>.

By default, the viewer hides spans whose parent was not seen by the viewer. To show those spans as roots instead, add `-allow-unknown-parents`.

```bash
go run . -allow-unknown-parents
```

## Development

Generate templ code:

```bash
mise run templ
```

Run with templ live reload:

```bash
mise run web
```
