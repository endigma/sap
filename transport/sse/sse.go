package sse

import (
	"fmt"
	"net/http"

	"github.com/endigma/sap"
	"google.golang.org/protobuf/encoding/protojson"
)

// NewHandler creates an HTTP handler that streams hub records as server-sent events.
func NewHandler(hub *sap.Hub) http.Handler {
	if hub == nil {
		panic("sap/transport/sse: nil Hub")
	}
	marshaler := protojson.MarshalOptions{UseProtoNames: true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported by responseWriter", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		records, cancel := hub.Subscribe(1024)
		defer cancel()

		if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
			return
		}
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case record, ok := <-records:
				if !ok {
					return
				}
				payload, err := marshaler.Marshal(record)
				if err != nil {
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})
}
