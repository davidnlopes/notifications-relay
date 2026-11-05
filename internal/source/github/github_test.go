package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"testing"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

const secret = "test-secret"

func delivery(eventName, body string) (http.Header, []byte) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	header := http.Header{}
	header.Set("X-GitHub-Event", eventName)
	header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	return header, []byte(body)
}

func parseOne(t *testing.T, eventName, body string) event.Event {
	t.Helper()
	header, payload := delivery(eventName, body)
	events, err := New("gh", secret).Parse(header, payload)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Parse() returned %d events, want 1", len(events))
	}
	if events[0].Source != "gh" {
		t.Errorf("Source = %q, want the configured name", events[0].Source)
	}
	return events[0]
}

func TestParseRejectsBadSignatures(t *testing.T) {
	body := `{"action":"published"}`
	tests := map[string]string{
		"missing":      "",
		"wrong prefix": "sha1=abcd",
		"not hex":      "sha256=zz",
		"wrong digest": "sha256=00",
	}
	for name, signature := range tests {
		t.Run(name, func(t *testing.T) {
			header := http.Header{}
			header.Set("X-GitHub-Event", "release")
			if signature != "" {
				header.Set("X-Hub-Signature-256", signature)
			}
			_, err := New("gh", secret).Parse(header, []byte(body))
			if !errors.Is(err, ErrInvalidSignature) {
				t.Errorf("Parse() error = %v, want ErrInvalidSignature", err)
			}
		})
	}
}

func TestParseRejectsBodyChangedAfterSigning(t *testing.T) {
	header, _ := delivery("release", `{"action":"published"}`)
	_, err := New("gh", secret).Parse(header, []byte(`{"action":"deleted"}`))
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("Parse() error = %v, want ErrInvalidSignature", err)
	}
}

func TestParseIgnoresUnhandledDeliveries(t *testing.T) {
	tests := map[string]struct{ event, body string }{
		"ping":                {"ping", `{"zen":"keep it logically awesome"}`},
		"pull request edited": {"pull_request", `{"action":"edited"}`},
		"release created":     {"release", `{"action":"created"}`},
		"workflow requested":  {"workflow_run", `{"action":"requested"}`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			header, body := delivery(test.event, test.body)
			events, err := New("gh", secret).Parse(header, body)
			if err != nil || len(events) != 0 {
				t.Errorf("Parse() = %v, %v, want no events and no error", events, err)
			}
		})
	}
}

func TestParseReturnsErrorForInvalidJSON(t *testing.T) {
	header, body := delivery("release", `{`)
	if _, err := New("gh", secret).Parse(header, body); err == nil {
		t.Error("Parse() error = nil, want an error")
	}
}

func pullRequestPayload(action, extra string, merged bool) string {
	mergedValue := "false"
	if merged {
		mergedValue = "true"
	}
	return `{"action":"` + action + `",` + extra + `
		"pull_request":{"number":7,"title":"Add thing","html_url":"https://example.test/pr/7","draft":false,
			"merged":` + mergedValue + `,"user":{"login":"author-bot"},"base":{"ref":"main"}},
		"repository":{"full_name":"acme/widgets","private":true},
		"sender":{"login":"sender-user"}}`
}

func TestParsePullRequest(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		extra     string
		merged    bool
		wantType  string
		wantLevel event.Level
		wantTitle string
	}{
		{"opened", "opened", "", false, "pull_request.opened", event.LevelInfo, "[acme/widgets] PR #7 opened: Add thing"},
		{"ready", "ready_for_review", "", false, "pull_request.ready_for_review", event.LevelInfo, "[acme/widgets] PR #7 ready for review: Add thing"},
		{"merged", "closed", "", true, "pull_request.merged", event.LevelSuccess, "[acme/widgets] PR #7 merged: Add thing"},
		{"closed", "closed", "", false, "pull_request.closed", event.LevelInfo, "[acme/widgets] PR #7 closed: Add thing"},
		{
			"review requested", "review_requested", `"requested_reviewer":{"login":"alice"},`, false,
			"pull_request.review_requested", event.LevelInfo, "[acme/widgets] PR #7 needs review: Add thing",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := parseOne(t, "pull_request", pullRequestPayload(test.action, test.extra, test.merged))
			if e.Type != test.wantType {
				t.Errorf("Type = %q, want %q", e.Type, test.wantType)
			}
			if e.Level != test.wantLevel {
				t.Errorf("Level = %q, want %q", e.Level, test.wantLevel)
			}
			if e.Title != test.wantTitle {
				t.Errorf("Title = %q, want %q", e.Title, test.wantTitle)
			}
			if e.URL != "https://example.test/pr/7" {
				t.Errorf("URL = %q", e.URL)
			}
			for key, want := range map[string]string{
				"repository": "acme/widgets", "private": "true", "sender": "sender-user",
				"author": "author-bot", "base_branch": "main", "draft": "false",
			} {
				if e.Attributes[key] != want {
					t.Errorf("Attributes[%q] = %q, want %q", key, e.Attributes[key], want)
				}
			}
		})
	}
}

func TestParseReviewRequestTargets(t *testing.T) {
	user := parseOne(t, "pull_request", pullRequestPayload("review_requested", `"requested_reviewer":{"login":"alice"},`, false))
	if user.Attributes["requested_reviewer"] != "alice" {
		t.Errorf("requested_reviewer = %q, want alice", user.Attributes["requested_reviewer"])
	}
	if _, present := user.Attributes["requested_team"]; present {
		t.Error("requested_team is set for a user review request")
	}

	team := parseOne(t, "pull_request", pullRequestPayload("review_requested", `"requested_team":{"slug":"core"},`, false))
	if team.Attributes["requested_team"] != "core" {
		t.Errorf("requested_team = %q, want core", team.Attributes["requested_team"])
	}
	if _, present := team.Attributes["requested_reviewer"]; present {
		t.Error("requested_reviewer is set for a team review request")
	}
}

func TestParseRelease(t *testing.T) {
	e := parseOne(t, "release", `{"action":"published",
		"release":{"tag_name":"v1.2.3","name":"Big release","html_url":"https://example.test/r","prerelease":false},
		"repository":{"full_name":"acme/widgets","private":false},"sender":{"login":"bot"}}`)

	if e.Type != "release.published" || e.Level != event.LevelSuccess {
		t.Errorf("Type = %q, Level = %q", e.Type, e.Level)
	}
	if e.Title != "[acme/widgets] Released v1.2.3" {
		t.Errorf("Title = %q", e.Title)
	}
	if e.Attributes["tag"] != "v1.2.3" || e.Attributes["prerelease"] != "false" {
		t.Errorf("Attributes = %v", e.Attributes)
	}
}

func TestParseWorkflowRunLevels(t *testing.T) {
	tests := map[string]event.Level{
		"success":         event.LevelSuccess,
		"failure":         event.LevelFailure,
		"timed_out":       event.LevelFailure,
		"startup_failure": event.LevelFailure,
		"cancelled":       event.LevelInfo,
	}
	for conclusion, wantLevel := range tests {
		t.Run(conclusion, func(t *testing.T) {
			e := parseOne(t, "workflow_run", `{"action":"completed",
				"workflow_run":{"name":"CI","html_url":"https://example.test/run","conclusion":"`+conclusion+`",
					"event":"pull_request","head_branch":"feat/x","actor":{"login":"agent"}},
				"repository":{"full_name":"acme/widgets"},"sender":{"login":"agent"}}`)

			if e.Type != "workflow_run.completed" {
				t.Errorf("Type = %q", e.Type)
			}
			if e.Level != wantLevel {
				t.Errorf("Level = %q, want %q", e.Level, wantLevel)
			}
			for key, want := range map[string]string{
				"conclusion": conclusion, "workflow": "CI", "trigger": "pull_request", "branch": "feat/x", "actor": "agent",
			} {
				if e.Attributes[key] != want {
					t.Errorf("Attributes[%q] = %q, want %q", key, e.Attributes[key], want)
				}
			}
		})
	}
}
