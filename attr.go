// Package sap records span activity and publishes it to subscribers.
package sap

import (
	sapv1 "github.com/endigma/sap/gen/sap/v1"
	"google.golang.org/protobuf/proto"
)

// Attribute describes a key-value annotation attached to a span or event.
type Attribute = sapv1.Attribute

// Attr creates an attribute with optional display hints.
func Attr(key, value string, hints ...string) *Attribute {
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
