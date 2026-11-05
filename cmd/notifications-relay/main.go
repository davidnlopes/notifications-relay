// Command notifications-relay receives webhooks, filters them with configured
// routes, and posts the matching events to chat channels.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/davidnlopes/notifications-relay/internal/config"
	"github.com/davidnlopes/notifications-relay/internal/relay"
	"github.com/davidnlopes/notifications-relay/internal/sink/discord"
	"github.com/davidnlopes/notifications-relay/internal/source/github"
)

const (
	defaultConfigPath = "/etc/notifications-relay/config.yaml"
	sinkHTTPTimeout   = 5 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("relay stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	configPath := flag.String("config", defaultConfigPath, "path to the configuration file")
	checkOnly := flag.Bool("check", false, "validate the configuration and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath, os.LookupEnv)
	if err != nil {
		return err
	}
	if *checkOnly {
		fmt.Println("configuration is valid")
		return nil
	}

	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           buildHandler(cfg, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 * 1024,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveError := make(chan error, 1)
	go func() { serveError <- server.ListenAndServe() }()
	logger.Info("relay started", "listen", cfg.Server.Listen)

	select {
	case err := <-serveError:
		return err
	case <-ctx.Done():
	}

	logger.Info("relay stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serveError; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func buildHandler(cfg *config.Config, logger *slog.Logger) http.Handler {
	sources := make(map[string]relay.Source, len(cfg.Sources))
	for _, source := range cfg.Sources {
		switch source.Type {
		case config.SourceTypeGitHub:
			sources[source.Name] = github.New(source.Name, source.Secret)
		}
	}

	client := &http.Client{Timeout: sinkHTTPTimeout}
	sinks := make(map[string]relay.Sink, len(cfg.Sinks))
	for _, sink := range cfg.Sinks {
		switch sink.Type {
		case config.SinkTypeDiscord:
			sinks[sink.Name] = discord.New(sink.WebhookURL, client)
		}
	}

	return relay.NewHandler(relay.Options{
		Sources:     sources,
		Sinks:       sinks,
		Routes:      cfg.RelayRoutes(),
		MaxBodySize: cfg.Server.MaxBodySize,
		MaxInFlight: cfg.Server.MaxInFlight,
		Logger:      logger,
	})
}
