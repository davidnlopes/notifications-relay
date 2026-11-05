// Package github turns GitHub webhook deliveries into events.
package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

// ErrInvalidSignature means that the delivery was not signed with the configured secret.
var ErrInvalidSignature = errors.New("invalid webhook signature")

// Source verifies and parses the deliveries of one GitHub webhook.
type Source struct {
	name   string
	secret []byte
}

// New returns a source that verifies deliveries with secret.
func New(name, secret string) *Source {
	return &Source{name: name, secret: []byte(secret)}
}

// Parse verifies a delivery and returns its events. Deliveries for event types
// or actions that the relay does not handle return no events and no error.
func (s *Source) Parse(header http.Header, body []byte) ([]event.Event, error) {
	if !s.validSignature(header.Get("X-Hub-Signature-256"), body) {
		return nil, ErrInvalidSignature
	}

	var parse func([]byte) (event.Event, bool, error)
	switch header.Get("X-GitHub-Event") {
	case "pull_request":
		parse = parsePullRequest
	case "release":
		parse = parseRelease
	case "workflow_run":
		parse = parseWorkflowRun
	default:
		return nil, nil
	}

	e, ok, err := parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse %s payload: %w", header.Get("X-GitHub-Event"), err)
	}
	if !ok {
		return nil, nil
	}
	e.Source = s.name
	return []event.Event{e}, nil
}

func (s *Source) validSignature(signature string, body []byte) bool {
	digest, found := strings.CutPrefix(signature, "sha256=")
	if !found {
		return false
	}
	want, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), want)
}

type account struct {
	Login string `json:"login"`
}

type repository struct {
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

func baseAttributes(repo repository, sender account) map[string]string {
	return map[string]string{
		"repository": repo.FullName,
		"private":    strconv.FormatBool(repo.Private),
		"sender":     sender.Login,
	}
}

func parsePullRequest(body []byte) (event.Event, bool, error) {
	var payload struct {
		Action      string `json:"action"`
		PullRequest struct {
			Number  int     `json:"number"`
			Title   string  `json:"title"`
			HTMLURL string  `json:"html_url"`
			Draft   bool    `json:"draft"`
			Merged  bool    `json:"merged"`
			User    account `json:"user"`
			Base    struct {
				Ref string `json:"ref"`
			} `json:"base"`
		} `json:"pull_request"`
		RequestedReviewer *account `json:"requested_reviewer"`
		RequestedTeam     *struct {
			Slug string `json:"slug"`
		} `json:"requested_team"`
		Repository repository `json:"repository"`
		Sender     account    `json:"sender"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return event.Event{}, false, err
	}

	pr := payload.PullRequest
	attributes := baseAttributes(payload.Repository, payload.Sender)
	attributes["author"] = pr.User.Login
	attributes["base_branch"] = pr.Base.Ref
	attributes["draft"] = strconv.FormatBool(pr.Draft)

	e := event.Event{
		Attributes: attributes,
		URL:        pr.HTMLURL,
		Level:      event.LevelInfo,
	}
	heading := func(verb string) string {
		return fmt.Sprintf("[%s] PR #%d %s: %s", payload.Repository.FullName, pr.Number, verb, pr.Title)
	}

	switch payload.Action {
	case "opened", "reopened", "ready_for_review":
		e.Type = "pull_request." + payload.Action
		e.Title = heading(strings.ReplaceAll(payload.Action, "_", " "))
		e.Text = "Author: " + pr.User.Login
	case "closed":
		if pr.Merged {
			e.Type = "pull_request.merged"
			e.Title = heading("merged")
			e.Text = "Merged by: " + payload.Sender.Login
			e.Level = event.LevelSuccess
		} else {
			e.Type = "pull_request.closed"
			e.Title = heading("closed")
			e.Text = "Closed by: " + payload.Sender.Login
		}
	case "review_requested":
		e.Type = "pull_request.review_requested"
		e.Title = heading("needs review")
		var reviewer string
		switch {
		case payload.RequestedReviewer != nil:
			attributes["requested_reviewer"] = payload.RequestedReviewer.Login
			reviewer = payload.RequestedReviewer.Login
		case payload.RequestedTeam != nil:
			attributes["requested_team"] = payload.RequestedTeam.Slug
			reviewer = "team " + payload.RequestedTeam.Slug
		}
		e.Text = fmt.Sprintf("Reviewer: %s\nRequested by: %s", reviewer, payload.Sender.Login)
	default:
		return event.Event{}, false, nil
	}
	return e, true, nil
}

func parseRelease(body []byte) (event.Event, bool, error) {
	var payload struct {
		Action  string `json:"action"`
		Release struct {
			TagName    string `json:"tag_name"`
			Name       string `json:"name"`
			HTMLURL    string `json:"html_url"`
			Prerelease bool   `json:"prerelease"`
		} `json:"release"`
		Repository repository `json:"repository"`
		Sender     account    `json:"sender"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return event.Event{}, false, err
	}
	if payload.Action != "published" {
		return event.Event{}, false, nil
	}

	release := payload.Release
	attributes := baseAttributes(payload.Repository, payload.Sender)
	attributes["tag"] = release.TagName
	attributes["prerelease"] = strconv.FormatBool(release.Prerelease)

	return event.Event{
		Type:       "release.published",
		Attributes: attributes,
		Title:      fmt.Sprintf("[%s] Released %s", payload.Repository.FullName, release.TagName),
		URL:        release.HTMLURL,
		Text:       release.Name,
		Level:      event.LevelSuccess,
	}, true, nil
}

func parseWorkflowRun(body []byte) (event.Event, bool, error) {
	var payload struct {
		Action      string `json:"action"`
		WorkflowRun struct {
			Name       string  `json:"name"`
			HTMLURL    string  `json:"html_url"`
			Conclusion string  `json:"conclusion"`
			Event      string  `json:"event"`
			HeadBranch string  `json:"head_branch"`
			Actor      account `json:"actor"`
		} `json:"workflow_run"`
		Repository repository `json:"repository"`
		Sender     account    `json:"sender"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return event.Event{}, false, err
	}
	if payload.Action != "completed" {
		return event.Event{}, false, nil
	}

	run := payload.WorkflowRun
	attributes := baseAttributes(payload.Repository, payload.Sender)
	attributes["workflow"] = run.Name
	attributes["conclusion"] = run.Conclusion
	attributes["trigger"] = run.Event
	attributes["branch"] = run.HeadBranch
	attributes["actor"] = run.Actor.Login

	level := event.LevelInfo
	switch run.Conclusion {
	case "success":
		level = event.LevelSuccess
	case "failure", "timed_out", "startup_failure":
		level = event.LevelFailure
	}

	return event.Event{
		Type:       "workflow_run.completed",
		Attributes: attributes,
		Title:      fmt.Sprintf("[%s] %s: %s", payload.Repository.FullName, run.Name, run.Conclusion),
		URL:        run.HTMLURL,
		Text:       fmt.Sprintf("Branch: %s\nTrigger: %s\nActor: %s", run.HeadBranch, run.Event, run.Actor.Login),
		Level:      level,
	}, true, nil
}
