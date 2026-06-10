// Package sap records span activity and publishes it to subscribers.
package sap

import (
	"strconv"
	"time"

	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/proto"
)

// Attribute describes a key-value annotation attached to a span or event.
type Attribute = sapv1.Attribute

// String creates a string attribute with optional display hints.
func String(key, value string, hints ...string) *Attribute {
	return sapv1.Attribute_builder{
		Key:          new(key),
		Value:        new(value),
		DisplayHints: dedupeStrings(hints),
	}.Build()
}

// LabeledAttr creates an attribute with a display label and optional display hints.
func LabeledAttr(key, label, value string, hints ...string) *Attribute {
	return sapv1.Attribute_builder{
		Key:          new(key),
		Label:        new(label),
		Value:        new(value),
		DisplayHints: dedupeStrings(hints),
	}.Build()
}

// Bool creates a boolean attribute with optional display hints.
func Bool(key string, value bool, hints ...string) *Attribute {
	return String(key, strconv.FormatBool(value), hints...)
}

// Int creates an integer attribute with optional display hints.
func Int(key string, value int, hints ...string) *Attribute {
	return String(key, strconv.Itoa(value), hints...)
}

// Int64 creates an integer attribute with optional display hints.
func Int64(key string, value int64, hints ...string) *Attribute {
	return String(key, strconv.FormatInt(value, 10), hints...)
}

// Float64 creates a floating-point attribute with optional display hints.
func Float64(key string, value float64, hints ...string) *Attribute {
	return String(key, strconv.FormatFloat(value, 'g', -1, 64), hints...)
}

// Duration creates a duration attribute with optional display hints.
func Duration(key string, value time.Duration, hints ...string) *Attribute {
	return String(key, value.String(), hints...)
}

func cloneAttributes(attrs []*Attribute) []*sapv1.Attribute {
	if len(attrs) == 0 {
		return nil
	}
	cloned := make([]*sapv1.Attribute, 0, len(attrs))
	for _, attr := range attrs {
		if attr == nil {
			continue
		}
		cloned = append(cloned, proto.CloneOf(attr))
	}
	return cloned
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
