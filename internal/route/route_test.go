package route

import (
	"slices"
	"testing"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

func TestMatches(t *testing.T) {
	e := event.Event{
		Source:     "github",
		Type:       "pull_request.review_requested",
		Attributes: map[string]string{"requested_reviewer": "alice", "sender": "bob"},
	}

	tests := []struct {
		name  string
		route Route
		want  bool
	}{
		{"empty route matches everything", Route{}, true},
		{"matching source", Route{Sources: []string{"github"}}, true},
		{"other source", Route{Sources: []string{"gitlab"}}, false},
		{"matching type", Route{Types: []string{"release.published", e.Type}}, true},
		{"other type", Route{Types: []string{"release.published"}}, false},
		{"where matches", Route{Where: Conditions{"requested_reviewer": {"alice"}}}, true},
		{"where has another value", Route{Where: Conditions{"requested_reviewer": {"carol"}}}, false},
		{"where attribute is absent", Route{Where: Conditions{"conclusion": {"failure"}}}, false},
		{
			"every where attribute must match",
			Route{Where: Conditions{"requested_reviewer": {"alice"}, "sender": {"carol"}}},
			false,
		},
		{"unless rejects", Route{Unless: Conditions{"sender": {"bob"}}}, false},
		{"unless has another value", Route{Unless: Conditions{"sender": {"dependabot[bot]"}}}, true},
		{"unless attribute is absent", Route{Unless: Conditions{"draft": {"true"}}}, true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.route.Matches(e); got != test.want {
				t.Errorf("Matches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestSinksReturnsEachMatchingSinkOnce(t *testing.T) {
	routes := []Route{
		{Types: []string{"a"}, Sinks: []string{"one", "two"}},
		{Types: []string{"b"}, Sinks: []string{"three"}},
		{Types: []string{"a"}, Sinks: []string{"two", "four"}},
	}

	got := Sinks(routes, event.Event{Type: "a"})

	if want := []string{"one", "two", "four"}; !slices.Equal(got, want) {
		t.Errorf("Sinks() = %v, want %v", got, want)
	}
}
