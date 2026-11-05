// Package config loads and validates the relay configuration file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/davidnlopes/notifications-relay/internal/route"
)

const (
	SourceTypeGitHub = "github"
	SinkTypeDiscord  = "discord"
)

const (
	defaultListen      = ":8080"
	defaultMaxBodySize = 1 << 20
	defaultMaxInFlight = 16
)

// A source name becomes part of the webhook path.
var sourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config is the relay configuration. Secrets stay out of the file: each secret
// field names the environment variable that holds the value.
type Config struct {
	Server  Server   `yaml:"server"`
	Sources []Source `yaml:"sources"`
	Sinks   []Sink   `yaml:"sinks"`
	Routes  []Route  `yaml:"routes"`
}

// Server holds the HTTP server settings.
type Server struct {
	Listen      string `yaml:"listen"`
	MaxBodySize int64  `yaml:"max_body_bytes"`
	MaxInFlight int    `yaml:"max_in_flight"`
}

// Source receives webhooks at /hooks/<name>.
type Source struct {
	Name      string `yaml:"name"`
	Type      string `yaml:"type"`
	SecretEnv string `yaml:"secret_env"`
	// Secret holds the value read from SecretEnv.
	Secret string `yaml:"-"`
}

// Sink is a destination for events.
type Sink struct {
	Name          string `yaml:"name"`
	Type          string `yaml:"type"`
	WebhookURLEnv string `yaml:"webhook_url_env"`
	// WebhookURL holds the value read from WebhookURLEnv.
	WebhookURL string `yaml:"-"`
}

// Route sends matching events to sinks.
type Route struct {
	Name    string           `yaml:"name"`
	Sources []string         `yaml:"sources"`
	Types   []string         `yaml:"types"`
	Where   route.Conditions `yaml:"where"`
	Unless  route.Conditions `yaml:"unless"`
	Sinks   []string         `yaml:"sinks"`
}

// Load reads the file at path, fills in defaults, and resolves secrets with lookupEnv.
func Load(path string, lookupEnv func(string) (string, bool)) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Server.Listen == "" {
		cfg.Server.Listen = defaultListen
	}
	if cfg.Server.MaxBodySize == 0 {
		cfg.Server.MaxBodySize = defaultMaxBodySize
	}
	if cfg.Server.MaxInFlight == 0 {
		cfg.Server.MaxInFlight = defaultMaxInFlight
	}

	if err := cfg.validate(lookupEnv); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// RelayRoutes converts the configured routes for the router.
func (c *Config) RelayRoutes() []route.Route {
	routes := make([]route.Route, 0, len(c.Routes))
	for _, r := range c.Routes {
		routes = append(routes, route.Route{
			Name:    r.Name,
			Sources: r.Sources,
			Types:   r.Types,
			Where:   r.Where,
			Unless:  r.Unless,
			Sinks:   r.Sinks,
		})
	}
	return routes
}

func (c *Config) validate(lookupEnv func(string) (string, bool)) error {
	var problems []error
	report := func(format string, args ...any) {
		problems = append(problems, fmt.Errorf(format, args...))
	}
	secret := func(owner, variable string) string {
		if variable == "" {
			report("%s: the environment variable name is required", owner)
			return ""
		}
		value, _ := lookupEnv(variable)
		if value == "" {
			report("%s: environment variable %s is not set", owner, variable)
		}
		return value
	}

	if c.Server.MaxBodySize < 0 {
		report("server: max_body_bytes must be positive")
	}
	if c.Server.MaxInFlight < 0 {
		report("server: max_in_flight must be positive")
	}

	if len(c.Sources) == 0 {
		report("at least one source is required")
	}
	var sources []string
	for i := range c.Sources {
		source := &c.Sources[i]
		owner := fmt.Sprintf("source %q", source.Name)
		if !sourceName.MatchString(source.Name) {
			report("%s: the name must match %s", owner, sourceName)
		}
		if slices.Contains(sources, source.Name) {
			report("%s: the name is used more than once", owner)
		}
		sources = append(sources, source.Name)
		if source.Type != SourceTypeGitHub {
			report("%s: unknown type %q", owner, source.Type)
		}
		source.Secret = secret(owner, source.SecretEnv)
	}

	if len(c.Sinks) == 0 {
		report("at least one sink is required")
	}
	var sinks []string
	for i := range c.Sinks {
		sink := &c.Sinks[i]
		owner := fmt.Sprintf("sink %q", sink.Name)
		if sink.Name == "" {
			report("%s: the name is required", owner)
		}
		if slices.Contains(sinks, sink.Name) {
			report("%s: the name is used more than once", owner)
		}
		sinks = append(sinks, sink.Name)
		if sink.Type != SinkTypeDiscord {
			report("%s: unknown type %q", owner, sink.Type)
		}
		sink.WebhookURL = secret(owner, sink.WebhookURLEnv)
	}

	if len(c.Routes) == 0 {
		report("at least one route is required")
	}
	for i, r := range c.Routes {
		owner := fmt.Sprintf("route %d (%q)", i, r.Name)
		if len(r.Sinks) == 0 {
			report("%s: at least one sink is required", owner)
		}
		for _, name := range r.Sinks {
			if !slices.Contains(sinks, name) {
				report("%s: unknown sink %q", owner, name)
			}
		}
		for _, name := range r.Sources {
			if !slices.Contains(sources, name) {
				report("%s: unknown source %q", owner, name)
			}
		}
	}
	return errors.Join(problems...)
}
