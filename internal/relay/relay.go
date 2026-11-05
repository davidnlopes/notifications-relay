// Package relay is the HTTP server that moves events from sources to sinks.
package relay

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/davidnlopes/notifications-relay/internal/event"
	"github.com/davidnlopes/notifications-relay/internal/route"
)

// deliveryTimeout keeps fan-out inside the 10 seconds that GitHub waits for a response.
const deliveryTimeout = 8 * time.Second

// Source verifies a webhook delivery and turns it into events.
type Source interface {
	Parse(header http.Header, body []byte) ([]event.Event, error)
}

// Sink delivers an event to a destination.
type Sink interface {
	Send(ctx context.Context, e event.Event) error
}

// Options are the settings of a Handler.
type Options struct {
	Sources     map[string]Source
	Sinks       map[string]Sink
	Routes      []route.Route
	MaxBodySize int64
	MaxInFlight int
	Logger      *slog.Logger
}

// NewHandler returns the relay HTTP API:
//
//	POST /hooks/{source}  receives a webhook delivery
//	GET  /healthz         reports that the process is serving requests
func NewHandler(options Options) http.Handler {
	h := &handler{options: options, inFlight: make(chan struct{}, options.MaxInFlight)}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/{source}", h.receive)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

type handler struct {
	options  Options
	inFlight chan struct{}
}

func (h *handler) receive(w http.ResponseWriter, r *http.Request) {
	// Bodies are read before they can be verified, so this cap is what bounds
	// the memory that unauthenticated requests can use.
	select {
	case h.inFlight <- struct{}{}:
		defer func() { <-h.inFlight }()
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	sourceName := r.PathValue("source")
	source, known := h.options.Sources[sourceName]
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	logger := h.options.Logger.With("source", sourceName)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.options.MaxBodySize))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	events, err := source.Parse(r.Header, body)
	if err != nil {
		logger.Warn("rejected delivery", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), deliveryTimeout)
	defer cancel()

	if !h.dispatch(ctx, logger, events) {
		// A failed status makes the failure visible to the sender, and lets the
		// operator redeliver the webhook where the sender supports that.
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// dispatch reports whether every matching sink accepted its events.
func (h *handler) dispatch(ctx context.Context, logger *slog.Logger, events []event.Event) bool {
	var (
		group     sync.WaitGroup
		mutex     sync.Mutex
		succeeded = true
	)
	for _, e := range events {
		sinks := route.Sinks(h.options.Routes, e)
		logger.Info("received event", "type", e.Type, "sinks", sinks)
		for _, name := range sinks {
			sink := h.options.Sinks[name]
			group.Add(1)
			go func() {
				defer group.Done()
				if err := sink.Send(ctx, e); err != nil {
					logger.Error("delivery failed", "type", e.Type, "sink", name, "error", err)
					mutex.Lock()
					succeeded = false
					mutex.Unlock()
				}
			}()
		}
	}
	group.Wait()
	return succeeded
}
