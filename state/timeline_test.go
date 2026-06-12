package state

import (
	"testing"
	"time"
)

func TestTimelineItems(t *testing.T) {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	t.Run("orders events and children chronologically", func(t *testing.T) {
		span := &SpanState{
			Events: []*EventState{
				{Name: "early", At: base},
				{Name: "late", At: base.Add(8 * time.Second)},
			},
			Children: []*SpanState{
				{SpanID: "child", Name: "child", StartedAt: base.Add(1 * time.Second)},
			},
		}
		items := TimelineItems(span)
		if len(items) != 3 {
			t.Fatalf("got %d items, want 3", len(items))
		}
		if items[0].Event == nil || items[0].Event.Name != "early" {
			t.Fatalf("items[0] = %+v", items[0])
		}
		if items[1].Span == nil || items[1].Span.Name != "child" {
			t.Fatalf("items[1] = %+v", items[1])
		}
		if items[2].Event == nil || items[2].Event.Name != "late" {
			t.Fatalf("items[2] = %+v", items[2])
		}
	})

	t.Run("places entries without timestamps last", func(t *testing.T) {
		span := &SpanState{
			Events: []*EventState{
				{Name: "no time"},
				{Name: "timed", At: base},
			},
		}
		items := TimelineItems(span)
		if items[0].Event.Name != "timed" || items[1].Event.Name != "no time" {
			t.Fatalf("items = %+v", items)
		}
	})

	t.Run("keeps insertion order for equal timestamps", func(t *testing.T) {
		span := &SpanState{
			Events: []*EventState{
				{Name: "first", At: base},
				{Name: "second", At: base},
			},
			Children: []*SpanState{
				{SpanID: "child", StartedAt: base},
			},
		}
		items := TimelineItems(span)
		if items[0].Event.Name != "first" || items[1].Event.Name != "second" || items[2].Span == nil {
			t.Fatalf("items = %+v", items)
		}
	})

	t.Run("is empty for a nil span", func(t *testing.T) {
		if items := TimelineItems(nil); items != nil {
			t.Fatalf("items = %+v, want nil", items)
		}
	})
}
