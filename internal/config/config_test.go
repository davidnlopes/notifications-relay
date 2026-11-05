package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validConfig = `
sources:
  - name: github
    type: github
    secret_env: GITHUB_SECRET
sinks:
  - name: reviews
    type: discord
    webhook_url_env: DISCORD_REVIEWS
routes:
  - name: my-reviews
    sources: [github]
    types: [pull_request.review_requested]
    where:
      requested_reviewer: [alice]
    unless:
      sender: ["dependabot[bot]"]
    sinks: [reviews]
`

func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}

var validEnv = map[string]string{"GITHUB_SECRET": "s3cret", "DISCORD_REVIEWS": "https://example.test/hook"}

func load(t *testing.T, content string, env map[string]string) (*Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, lookup(env))
}

func TestLoadValidConfig(t *testing.T) {
	cfg, err := load(t, validConfig, validEnv)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Listen != defaultListen {
		t.Errorf("Listen = %q, want the default", cfg.Server.Listen)
	}
	if cfg.Server.MaxBodySize != defaultMaxBodySize || cfg.Server.MaxInFlight != defaultMaxInFlight {
		t.Errorf("Server = %+v, want the defaults", cfg.Server)
	}
	if cfg.Sources[0].Secret != "s3cret" {
		t.Errorf("Secret = %q", cfg.Sources[0].Secret)
	}
	if cfg.Sinks[0].WebhookURL != "https://example.test/hook" {
		t.Errorf("WebhookURL = %q", cfg.Sinks[0].WebhookURL)
	}

	routes := cfg.RelayRoutes()
	if len(routes) != 1 || routes[0].Where["requested_reviewer"][0] != "alice" || routes[0].Unless["sender"][0] != "dependabot[bot]" {
		t.Errorf("RelayRoutes() = %+v", routes)
	}
}

func TestLoadKeepsConfiguredServerValues(t *testing.T) {
	cfg, err := load(t, "server: {listen: \":9000\", max_body_bytes: 2048, max_in_flight: 4}\n"+validConfig, validEnv)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != ":9000" || cfg.Server.MaxBodySize != 2048 || cfg.Server.MaxInFlight != 4 {
		t.Errorf("Server = %+v", cfg.Server)
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		env     map[string]string
		want    string
	}{
		{"empty file", "", validEnv, "at least one source"},
		{"unknown field", validConfig + "\nunknown: true\n", validEnv, "unknown"},
		{"missing secret", validConfig, map[string]string{"DISCORD_REVIEWS": "x"}, "GITHUB_SECRET is not set"},
		{"missing webhook URL", validConfig, map[string]string{"GITHUB_SECRET": "x"}, "DISCORD_REVIEWS is not set"},
		{"unknown source type", strings.Replace(validConfig, "type: github", "type: gitlab", 1), validEnv, `unknown type "gitlab"`},
		{"unknown sink type", strings.Replace(validConfig, "type: discord", "type: smoke", 1), validEnv, `unknown type "smoke"`},
		{"unsafe source name", strings.Replace(validConfig, "name: github", "name: a/b", 1), validEnv, "the name must match"},
		{"route to unknown sink", strings.Replace(validConfig, "sinks: [reviews]", "sinks: [nope]", 1), validEnv, `unknown sink "nope"`},
		{"route from unknown source", strings.Replace(validConfig, "sources: [github]", "sources: [nope]", 1), validEnv, `unknown source "nope"`},
		{"route without sinks", strings.Replace(validConfig, "sinks: [reviews]", "sinks: []", 1), validEnv, "at least one sink is required"},
		{"secret name missing", strings.Replace(validConfig, "secret_env: GITHUB_SECRET", "", 1), validEnv, "variable name is required"},
		{"negative body size", "server: {max_body_bytes: -1}\n" + validConfig, validEnv, "max_body_bytes"},
		{"negative in-flight", "server: {max_in_flight: -1}\n" + validConfig, validEnv, "max_in_flight"},
		{
			"duplicate source",
			strings.Replace(validConfig, "sinks:\n", "  - {name: github, type: github, secret_env: GITHUB_SECRET}\nsinks:\n", 1),
			validEnv, "used more than once",
		},
		{
			"duplicate sink",
			strings.Replace(validConfig, "routes:\n", "  - {name: reviews, type: discord, webhook_url_env: DISCORD_REVIEWS}\nroutes:\n", 1),
			validEnv, "used more than once",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := load(t, test.content, test.env)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Errorf("Load() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestLoadErrorsDoNotContainSecretValues(t *testing.T) {
	_, err := load(t, strings.Replace(validConfig, "sinks: [reviews]", "sinks: [nope]", 1), validEnv)
	if err == nil {
		t.Fatal("Load() error = nil")
	}
	if strings.Contains(err.Error(), "s3cret") || strings.Contains(err.Error(), "example.test/hook") {
		t.Errorf("error contains a secret value: %v", err)
	}
}

func TestLoadReportsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml"), lookup(nil)); err == nil {
		t.Error("Load() error = nil, want an error")
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	env := func(string) (string, bool) { return "value", true }
	if _, err := Load(filepath.Join("..", "..", "config.example.yaml"), env); err != nil {
		t.Errorf("config.example.yaml: %v", err)
	}
}
