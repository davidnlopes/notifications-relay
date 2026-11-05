package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/davidnlopes/notifications-relay/internal/event"
	"github.com/davidnlopes/notifications-relay/internal/route"
)

type fakeSource struct {
	events []event.Event
	err    error
}

func (f fakeSource) Parse(http.Header, []byte) ([]event.Event, error) { return f.events, f.err }

type fakeSink struct {
	mutex    sync.Mutex
	received []event.Event
	err      error
}

func (f *fakeSink) Send(_ context.Context, e event.Event) error {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.received = append(f.received, e)
	return f.err
}

func newHandler(source Source, sinks map[string]Sink, routes []route.Route) http.Handler {
	return NewHandler(Options{
		Sources:     map[string]Source{"test": source},
		Sinks:       sinks,
		Routes:      routes,
		MaxBodySize: 16,
		MaxInFlight: 2,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func post(handler http.Handler, path, body string) int {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return recorder.Code
}

func TestHealthz(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler(fakeSource{}, nil, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
}

func TestReceiveRoutesEventsToMatchingSinks(t *testing.T) {
	matching, other := &fakeSink{}, &fakeSink{}
	handler := newHandler(
		fakeSource{events: []event.Event{{Type: "a"}}},
		map[string]Sink{"matching": matching, "other": other},
		[]route.Route{
			{Types: []string{"a"}, Sinks: []string{"matching"}},
			{Types: []string{"b"}, Sinks: []string{"other"}},
		},
	)

	if code := post(handler, "/hooks/test", "{}"); code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", code)
	}
	if len(matching.received) != 1 {
		t.Errorf("matching sink received %d events, want 1", len(matching.received))
	}
	if len(other.received) != 0 {
		t.Errorf("other sink received %d events, want 0", len(other.received))
	}
}

func TestReceiveStatuses(t *testing.T) {
	failing := &fakeSink{err: errors.New("down")}
	matchAll := []route.Route{{Sinks: []string{"failing"}}}
	oneEvent := []event.Event{{Type: "a"}}

	tests := []struct {
		name   string
		source Source
		routes []route.Route
		path   string
		body   string
		want   int
	}{
		{"unknown source", fakeSource{}, nil, "/hooks/absent", "{}", http.StatusNotFound},
		{"body too large", fakeSource{}, nil, "/hooks/test", strings.Repeat("x", 17), http.StatusRequestEntityTooLarge},
		{"rejected by source", fakeSource{err: errors.New("bad signature")}, nil, "/hooks/test", "{}", http.StatusBadRequest},
		{"no events", fakeSource{}, matchAll, "/hooks/test", "{}", http.StatusNoContent},
		{"no matching route", fakeSource{events: oneEvent}, nil, "/hooks/test", "{}", http.StatusNoContent},
		{"sink failure", fakeSource{events: oneEvent}, matchAll, "/hooks/test", "{}", http.StatusBadGateway},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newHandler(test.source, map[string]Sink{"failing": failing}, test.routes)
			if code := post(handler, test.path, test.body); code != test.want {
				t.Errorf("status = %d, want %d", code, test.want)
			}
		})
	}
}

func TestReceiveRejectsOtherMethods(t *testing.T) {
	recorder := httptest.NewRecorder()
	newHandler(fakeSource{}, nil, nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/hooks/test", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", recorder.Code)
	}
}

type blockingSource struct {
	entered chan struct{}
	release chan struct{}
}

func (b blockingSource) Parse(http.Header, []byte) ([]event.Event, error) {
	b.entered <- struct{}{}
	<-b.release
	return nil, nil
}

func TestReceiveShedsRequestsAboveTheInFlightLimit(t *testing.T) {
	source := blockingSource{entered: make(chan struct{}), release: make(chan struct{})}
	handler := newHandler(source, nil, nil)

	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			post(handler, "/hooks/test", "{}")
		}()
		<-source.entered
	}

	if code := post(handler, "/hooks/test", "{}"); code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}

	close(source.release)
	group.Wait()
}
