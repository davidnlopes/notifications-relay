// Package route decides which sinks receive an event.
package route

import (
	"slices"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

// Conditions maps an attribute name to the values it can match.
type Conditions map[string][]string

// Route sends the events it matches to Sinks.
type Route struct {
	Name string
	// Sources and Types match any value when empty.
	Sources []string
	Types   []string
	// Where requires every listed attribute to have one of its values.
	Where Conditions
	// Unless rejects an event when any listed attribute has one of its values.
	Unless Conditions
	Sinks  []string
}

// Matches reports whether the route accepts the event.
func (r Route) Matches(e event.Event) bool {
	if len(r.Sources) > 0 && !slices.Contains(r.Sources, e.Source) {
		return false
	}
	if len(r.Types) > 0 && !slices.Contains(r.Types, e.Type) {
		return false
	}
	for attribute, values := range r.Where {
		actual, present := e.Attributes[attribute]
		if !present || !slices.Contains(values, actual) {
			return false
		}
	}
	for attribute, values := range r.Unless {
		if actual, present := e.Attributes[attribute]; present && slices.Contains(values, actual) {
			return false
		}
	}
	return true
}

// Sinks returns the sink names for all matching routes, without duplicates, in route order.
func Sinks(routes []Route, e event.Event) []string {
	var sinks []string
	for _, r := range routes {
		if !r.Matches(e) {
			continue
		}
		for _, sink := range r.Sinks {
			if !slices.Contains(sinks, sink) {
				sinks = append(sinks, sink)
			}
		}
	}
	return sinks
}
