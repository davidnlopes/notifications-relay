// Package event defines the source-independent form of a notification.
package event

// Level describes how an event should be presented.
type Level string

const (
	LevelInfo    Level = "info"
	LevelSuccess Level = "success"
	LevelFailure Level = "failure"
)

// Event is what a source produces, what routes match on, and what sinks render.
type Event struct {
	// Source is the name of the configured source that received the event.
	Source string
	// Type identifies the event within its source, for example "release.published".
	Type string
	// Attributes hold the source-specific values that routes can match on.
	Attributes map[string]string

	Title string
	URL   string
	Text  string
	Level Level
}
