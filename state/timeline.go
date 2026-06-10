package state

import (
	"cmp"
	"slices"
	"time"
)

// TimelineItem is one chronological entry in a span's timeline. Exactly one
// of Event or Span is set.
type TimelineItem struct {
	At    time.Time
	Index int
	Event *EventState
	Span  *SpanState
}

// TimelineItems returns the span's events and children merged into
// chronological order. Entries without a timestamp sort last; ties keep
// insertion order, events before children.
func TimelineItems(span *SpanState) []TimelineItem {
	if span == nil {
		return nil
	}
	items := make([]TimelineItem, 0, len(span.Events)+len(span.Children))
	for _, event := range span.Events {
		if event == nil {
			continue
		}
		items = append(items, TimelineItem{At: event.At, Index: len(items), Event: event})
	}
	for _, child := range span.Children {
		if child == nil {
			continue
		}
		items = append(items, TimelineItem{At: child.StartedAt, Index: len(items), Span: child})
	}
	slices.SortStableFunc(items, func(left, right TimelineItem) int {
		switch {
		case left.At.IsZero() && right.At.IsZero():
			return cmp.Compare(left.Index, right.Index)
		case left.At.IsZero():
			return 1
		case right.At.IsZero():
			return -1
		default:
			return left.At.Compare(right.At)
		}
	})
	return items
}
