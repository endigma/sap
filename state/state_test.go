package state

import (
	"fmt"
	"testing"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStoreClearOpenSpans(t *testing.T) {
	store := NewStore()
	closedRootID := "closed-root"
	openChildID := "open-child"
	openRootID := "open-root"
	closedChildID := "closed-child"
	now := time.Now()

	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &closedRootID, Name: &closedRootID}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: &closedRootID, EndedAt: timestamppb.New(now)}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &openChildID, ParentSpanId: &closedRootID, Name: &openChildID}.Build()}.Build())

	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &openRootID, Name: &openRootID}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &closedChildID, ParentSpanId: &openRootID, Name: &closedChildID}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: &closedChildID, EndedAt: timestamppb.New(now)}.Build()}.Build())

	store.ClearOpenSpans()
	roots := store.Roots()
	if len(roots) != 1 || roots[0].SpanID != closedRootID {
		t.Fatalf("roots = %+v, want only %s", roots, closedRootID)
	}
	if len(roots[0].Children) != 0 {
		t.Fatalf("closed root children = %+v, want open child pruned", roots[0].Children)
	}

	store.Apply(sapv1.Record_builder{SpanUpdated: sapv1.SpanUpdated_builder{TraceId: new("trace-1"), SpanId: &openRootID, Attributes: []*sapv1.Attribute{sapv1.Attribute_builder{Key: new("status"), Value: new("ignored")}.Build()}}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEvent: sapv1.SpanEvent_builder{TraceId: new("trace-1"), SpanId: &openChildID, Name: new("ignored")}.Build()}.Build())
	if len(store.Roots()) != 1 || len(store.Roots()[0].Children) != 0 {
		t.Fatalf("pruned open spans were updated after ClearOpenSpans: %+v", store.Roots())
	}
}

func TestStoreLimitRootsFIFO(t *testing.T) {
	store := NewStore()
	for i := 0; i < 30; i++ {
		rootID := fmt.Sprintf("root-%02d", i)
		childID := fmt.Sprintf("child-%02d", i)
		store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &rootID, Name: &rootID}.Build()}.Build())
		store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &childID, ParentSpanId: &rootID, Name: &childID}.Build()}.Build())
	}

	store.LimitRoots(25)
	roots := store.Roots()
	if len(roots) != 25 {
		t.Fatalf("got %d roots, want 25", len(roots))
	}
	if roots[0].SpanID != "root-05" || roots[24].SpanID != "root-29" {
		t.Fatalf("roots = %s ... %s, want root-05 ... root-29", roots[0].SpanID, roots[24].SpanID)
	}

	prunedRoot := "root-00"
	prunedChild := "child-00"
	store.Apply(sapv1.Record_builder{SpanUpdated: sapv1.SpanUpdated_builder{TraceId: new("trace-1"), SpanId: &prunedRoot, Attributes: []*sapv1.Attribute{sapv1.Attribute_builder{Key: new("status"), Value: new("ignored")}.Build()}}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: &prunedChild, EndedAt: timestamppb.New(time.Now())}.Build()}.Build())
	for _, root := range store.Roots() {
		if root.SpanID == prunedRoot {
			t.Fatalf("pruned root still present")
		}
		for _, child := range root.Children {
			if child.SpanID == prunedChild {
				t.Fatalf("pruned child still present")
			}
		}
	}
}

func TestStoreIgnoresUnknownParentSpansByDefault(t *testing.T) {
	store := NewStore()

	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("orphan"), ParentSpanId: new("missing-parent"), Name: new("orphan")}.Build()}.Build())
	if len(store.Roots()) != 0 {
		t.Fatalf("roots = %+v, want unknown-parent span ignored", store.Roots())
	}
}

func TestStoreAllowsUnknownParentSpansWhenConfigured(t *testing.T) {
	store := NewStoreWithOptions(StoreOptions{AllowUnknownParents: true})
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("orphan"), ParentSpanId: new("missing-parent"), Name: new("orphan")}.Build()}.Build())

	roots := store.Roots()
	if len(roots) != 1 || roots[0].SpanID != "orphan" || roots[0].ParentSpanID != "missing-parent" {
		t.Fatalf("roots = %+v, want orphan promoted to root", roots)
	}
}

func TestStoreIgnoresUnknownParentSpansWhenConfigured(t *testing.T) {
	store := NewStore()

	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("orphan"), ParentSpanId: new("missing-parent"), Name: new("orphan")}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("orphan-child"), ParentSpanId: new("orphan"), Name: new("orphan-child")}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanUpdated: sapv1.SpanUpdated_builder{TraceId: new("trace-1"), SpanId: new("orphan"), Attributes: []*sapv1.Attribute{sapv1.Attribute_builder{Key: new("status"), Value: new("ignored")}.Build()}}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEvent: sapv1.SpanEvent_builder{TraceId: new("trace-1"), SpanId: new("orphan"), Name: new("ignored")}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: new("orphan"), EndedAt: timestamppb.New(time.Now())}.Build()}.Build())
	if len(store.Roots()) != 0 {
		t.Fatalf("roots = %+v, want unknown-parent spans ignored", store.Roots())
	}

	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("root"), Name: new("root")}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("child"), ParentSpanId: new("root"), Name: new("child")}.Build()}.Build())
	roots := store.Roots()
	if len(roots) != 1 || roots[0].SpanID != "root" || len(roots[0].Children) != 1 || roots[0].Children[0].SpanID != "child" {
		t.Fatalf("roots = %+v, want known-parent child retained", roots)
	}
}

func TestStoreReconstructsAndClears(t *testing.T) {
	store := NewStore()
	startedAt := time.Now().Add(-2 * time.Second).UTC()
	eventAt := startedAt.Add(500 * time.Millisecond)
	endedAt := startedAt.Add(3 * time.Second)
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("root"), Name: new("root"), StartedAt: timestamppb.New(startedAt), Attributes: []*sapv1.Attribute{sapv1.Attribute_builder{Key: new("goal"), Value: new("ship")}.Build()}}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: new("child"), ParentSpanId: new("root"), Name: new("child")}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanUpdated: sapv1.SpanUpdated_builder{TraceId: new("trace-1"), SpanId: new("root"), Attributes: []*sapv1.Attribute{sapv1.Attribute_builder{Key: new("goal"), Value: new("ship now")}.Build(), sapv1.Attribute_builder{Key: new("status"), Value: new("running")}.Build()}}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEvent: sapv1.SpanEvent_builder{TraceId: new("trace-1"), SpanId: new("root"), Name: new("warn"), EventAt: timestamppb.New(eventAt), Severity: sapv1.SpanEvent_SEVERITY_WARN.Enum()}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: new("root"), EndedAt: timestamppb.New(endedAt), TerminalType: sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE.Enum()}.Build()}.Build())
	store.Apply(sapv1.Record_builder{SpanEvent: sapv1.SpanEvent_builder{TraceId: new("trace-1"), SpanId: new("root"), Name: new("ignored")}.Build()}.Build())

	roots := store.Roots()
	if len(roots) != 1 {
		t.Fatalf("got %d roots, want 1", len(roots))
	}
	root := roots[0]
	if len(root.Children) != 1 || root.Children[0].SpanID != "child" {
		t.Fatalf("children = %+v", root.Children)
	}
	if len(root.Attributes) != 2 || root.Attributes[0].GetValue() != "ship now" || root.Attributes[1].GetKey() != "status" {
		t.Fatalf("attributes = %+v", root.Attributes)
	}
	if len(root.Events) != 1 || root.Events[0].Name != "warn" || root.Events[0].Severity != sapv1.SpanEvent_SEVERITY_WARN {
		t.Fatalf("events = %+v", root.Events)
	}
	if !root.StartedAt.Equal(startedAt) || !root.EndedAt.Equal(endedAt) || !root.Events[0].At.Equal(eventAt) {
		t.Fatalf("timings = %+v %+v %+v", root.StartedAt, root.EndedAt, root.Events[0].At)
	}
	if !root.Ended || root.TerminalType != sapv1.SpanEnded_TERMINAL_TYPE_COMPLETE {
		t.Fatalf("end = %+v", root)
	}

	store.Clear()
	if len(store.Roots()) != 0 {
		t.Fatalf("roots after Clear = %+v", store.Roots())
	}
}

func TestStoreStaleTracking(t *testing.T) {
	start := func(store *Store, spanID, parentID string) {
		builder := sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &spanID, Name: &spanID}
		if parentID != "" {
			builder.ParentSpanId = &parentID
		}
		store.Apply(sapv1.Record_builder{SpanStarted: builder.Build()}.Build())
	}
	end := func(store *Store, spanID string) {
		store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: &spanID, EndedAt: timestamppb.New(time.Now())}.Build()}.Build())
	}

	t.Run("marks only open spans and keeps the earliest time", func(t *testing.T) {
		store := NewStore()
		start(store, "root", "")
		start(store, "open-child", "root")
		start(store, "closed-child", "root")
		end(store, "closed-child")

		first := time.Now()
		store.MarkOpenSpansStale(first)
		store.MarkOpenSpansStale(first.Add(time.Minute))

		stale := store.StaleOpenSpanIDs()
		if len(stale) != 2 {
			t.Fatalf("stale = %+v, want root and open-child", stale)
		}
		if at, ok := stale["open-child"]; !ok || !at.Equal(first) {
			t.Fatalf("open-child stale at %v, want %v", at, first)
		}
		if _, ok := stale["closed-child"]; ok {
			t.Fatal("closed span was marked stale")
		}
	})

	t.Run("drops marks when spans leave the store", func(t *testing.T) {
		store := NewStore()
		start(store, "old-root", "")
		store.MarkOpenSpansStale(time.Now())
		start(store, "new-root", "")
		store.LimitRoots(1)
		if len(store.StaleOpenSpanIDs()) != 0 {
			t.Fatalf("stale = %+v, want empty after eviction", store.StaleOpenSpanIDs())
		}
	})
}

func TestStoreFindSpan(t *testing.T) {
	t.Run("returns the span and its depth", func(t *testing.T) {
		store := NewStore()
		rootID, childID := "root", "child"
		store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &rootID, Name: &rootID}.Build()}.Build())
		store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &childID, ParentSpanId: &rootID, Name: &childID}.Build()}.Build())

		span, depth := store.FindSpan("child")
		if span == nil || span.SpanID != "child" || depth != 1 {
			t.Fatalf("FindSpan = %+v depth %d, want child at depth 1", span, depth)
		}
	})

	t.Run("returns nil for an unknown ID", func(t *testing.T) {
		span, _ := NewStore().FindSpan("missing")
		if span != nil {
			t.Fatalf("FindSpan = %+v, want nil", span)
		}
	})
}

func TestStoreHasOpenSpans(t *testing.T) {
	t.Run("is true while any span is in progress and false after it ends", func(t *testing.T) {
		store := NewStore()
		spanID := "root"
		store.Apply(sapv1.Record_builder{SpanStarted: sapv1.SpanStarted_builder{TraceId: new("trace-1"), SpanId: &spanID, Name: &spanID}.Build()}.Build())
		if !store.HasOpenSpans() {
			t.Fatal("open span not reported")
		}
		store.Apply(sapv1.Record_builder{SpanEnded: sapv1.SpanEnded_builder{TraceId: new("trace-1"), SpanId: &spanID, EndedAt: timestamppb.New(time.Now())}.Build()}.Build())
		if store.HasOpenSpans() {
			t.Fatal("ended span reported as open")
		}
	})
}
