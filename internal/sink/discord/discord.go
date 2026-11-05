// Package discord posts events to a Discord channel through an incoming webhook.
package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/davidnlopes/notifications-relay/internal/event"
)

// Discord rejects embeds above these lengths.
const (
	maxTitleRunes       = 256
	maxDescriptionRunes = 4096
)

const (
	colorInfo    = 0x5865F2
	colorSuccess = 0x57F287
	colorFailure = 0xED4245
)

// maxRetryWait caps how long a rate-limited send waits, because the caller
// is holding a webhook delivery open.
const maxRetryWait = 3 * time.Second

// Sink posts to one Discord webhook.
type Sink struct {
	webhookURL string
	client     *http.Client
}

// New returns a sink for webhookURL.
func New(webhookURL string, client *http.Client) *Sink {
	return &Sink{webhookURL: webhookURL, client: client}
}

type message struct {
	Embeds          []embed         `json:"embeds"`
	AllowedMentions allowedMentions `json:"allowed_mentions"`
}

type embed struct {
	Title       string `json:"title"`
	URL         string `json:"url,omitempty"`
	Description string `json:"description,omitempty"`
	Color       int    `json:"color"`
}

type allowedMentions struct {
	Parse []string `json:"parse"`
}

// Send posts the event, and retries once if Discord asks it to wait briefly.
func (s *Sink) Send(ctx context.Context, e event.Event) error {
	payload, err := json.Marshal(message{
		Embeds: []embed{{
			Title:       truncate(e.Title, maxTitleRunes),
			URL:         e.URL,
			Description: truncate(e.Text, maxDescriptionRunes),
			Color:       color(e.Level),
		}},
		// Event text comes from third parties, so it must not be able to ping @everyone.
		AllowedMentions: allowedMentions{Parse: []string{}},
	})
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	wait, err := s.post(ctx, payload)
	if err == nil || wait == 0 {
		return err
	}
	if wait > maxRetryWait {
		return fmt.Errorf("%w: retry needed after %s", err, wait)
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err = s.post(ctx, payload)
	return err
}

// post returns how long to wait before a retry when Discord rate-limits the request.
func (s *Sink) post(ctx context.Context, payload []byte) (time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(payload))
	if err != nil {
		return 0, errors.New("build discord request: invalid webhook URL")
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return 0, redact(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))

	if response.StatusCode == http.StatusTooManyRequests {
		wait, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64)
		if err != nil || wait <= 0 {
			wait = maxRetryWait.Seconds() + 1
		}
		return time.Duration(wait * float64(time.Second)), errors.New("discord rate limit reached")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return 0, fmt.Errorf("discord returned status %d", response.StatusCode)
	}
	return 0, nil
}

// redact removes the webhook URL from transport errors, because the URL contains the webhook token.
func redact(err error) error {
	var urlError *url.Error
	if errors.As(err, &urlError) {
		return fmt.Errorf("post to discord: %w", urlError.Err)
	}
	return errors.New("post to discord failed")
}

func color(level event.Level) int {
	switch level {
	case event.LevelSuccess:
		return colorSuccess
	case event.LevelFailure:
		return colorFailure
	default:
		return colorInfo
	}
}

func truncate(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	return string([]rune(text)[:maxRunes-1]) + "…"
}
