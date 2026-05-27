package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/a-h/templ"
	"github.com/endigma/sap/state"
	"github.com/endigma/sap/transport/sse"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "HTTP address for the web viewer")
	url := flag.String("url", "http://127.0.0.1:8080/live", "Sap SSE endpoint URL")
	maxRoots := flag.Int("roots", 50, "maximum root spans to retain")
	allowUnknownParents := flag.Bool("allow-unknown-parents", false, "show spans with non-empty parent IDs that are not currently known as roots")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	records, states := sse.Stream(ctx, *url)
	storeOptions := state.StoreOptions{AllowUnknownParents: *allowUnknownParents}
	viewer := newServer(*maxRoots, storeOptions)
	viewer.start(ctx, records, states)

	mux := http.NewServeMux()
	viewer.mount(mux)
	handler := templ.NewCSSMiddleware(mux, stylesheetClasses()...)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()

	log.Printf("sap web viewer listening on http://%s and reading %s", *addr, *url)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
