package main

import (
	"strings"
	"testing"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"github.com/endigma/sap/state"
)

func TestRenderRoots(t *testing.T) {
	t.Run("keeps a trailing event visible once the closed tree render is cached", func(t *testing.T) {
		now := time.Now()
		root := &state.SpanState{SpanID: "root", Name: "serve request", StartedAt: now.Add(-8 * time.Second)}
		roots := []*state.SpanState{root}
		cache := map[spanRenderCacheKey]string{}
		stale := map[string]time.Time{}

		renderRoots(roots, 80, "", now, stale, cache)
		root.Events = append(root.Events, &state.EventState{
			Name:     "recoverable.error",
			At:       now.Add(-time.Second),
			Severity: sapv1.SpanEvent_SEVERITY_ERROR,
		})
		renderRoots(roots, 80, "", now, stale, cache)
		root.Ended = true
		root.EndedAt = now

		first := renderRoots(roots, 80, "", now, stale, cache)
		cached := renderRoots(roots, 80, "", now, stale, cache)
		if !strings.Contains(first, "recoverable.error") || !strings.Contains(cached, "recoverable.error") {
			t.Fatalf("trailing event missing (first and cached renders):\n%s", cached)
		}
	})
}
