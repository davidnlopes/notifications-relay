package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

func TestSendPostsAnEmbedWithMentionsDisabled(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("invalid JSON: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := New(server.URL, server.Client()).Send(context.Background(), event.Event{
		Title: "Title @everyone", URL: "https://example.test", Text: "Text", Level: event.LevelFailure,
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	mentions := got["allowed_mentions"].(map[string]any)["parse"].([]any)
	if len(mentions) != 0 {
		t.Errorf("allowed_mentions.parse = %v, want an empty list", mentions)
	}
	first := got["embeds"].([]any)[0].(map[string]any)
	if first["title"] != "Title @everyone" || first["url"] != "https://example.test" || first["description"] != "Text" {
		t.Errorf("embed = %v", first)
	}
	if int(first["color"].(float64)) != colorFailure {
		t.Errorf("color = %v, want the failure color", first["color"])
	}
}

func TestSendTruncatesToDiscordLimits(t *testing.T) {
	var got message
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := New(server.URL, server.Client()).Send(context.Background(), event.Event{
		Title: strings.Repeat("é", maxTitleRunes+1),
		Text:  strings.Repeat("é", maxDescriptionRunes+1),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if n := utf8.RuneCountInString(got.Embeds[0].Title); n != maxTitleRunes {
		t.Errorf("title length = %d, want %d", n, maxTitleRunes)
	}
	if n := utf8.RuneCountInString(got.Embeds[0].Description); n != maxDescriptionRunes {
		t.Errorf("description length = %d, want %d", n, maxDescriptionRunes)
	}
}

func TestSendRetriesOnceAfterShortRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0.01")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := New(server.URL, server.Client()).Send(context.Background(), event.Event{Title: "t"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
}

func TestSendGivesUpOnLongRateLimit(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	if err := New(server.URL, server.Client()).Send(context.Background(), event.Event{Title: "t"}); err == nil {
		t.Fatal("Send() error = nil, want an error")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", calls.Load())
	}
}

func TestSendReturnsErrorForServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	err := New(server.URL, server.Client()).Send(context.Background(), event.Event{Title: "t"})
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("Send() error = %v, want the status code", err)
	}
}

func TestSendErrorsDoNotContainTheWebhookToken(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL + "/api/webhooks/1/secret-token"
	client := server.Client()
	server.Close()

	err := New(url, client).Send(context.Background(), event.Event{Title: "t"})
	if err == nil {
		t.Fatal("Send() error = nil, want a connection error")
	}
	if strings.Contains(err.Error(), "secret-token") {
		t.Errorf("error contains the token: %v", err)
	}
}
