# notifications-relay

A generic webhook relay in Go. It receives webhooks from sources, matches them against configured routes, and posts them to sinks.

## Rules for changes

- This is an open source project. Do not add assumptions about any specific deployment, cluster, domain, or person. Anything that varies between users belongs in the configuration file.
- Secrets never go in the configuration file. A secret field names the environment variable that holds the value.
- Keep the project small. It has one runtime dependency, for YAML. Discuss any new dependency before you add it.
- `internal/route` and `internal/relay` must not know about GitHub or Discord. Source-specific logic stays in `internal/source/*`, and sink-specific logic stays in `internal/sink/*`.
- Discord webhook URLs contain a token. Never log them or return them in an error.
- Verify a delivery's signature before you parse its payload.
- CI runs on GitHub-hosted runners. This repository is public, so do not use self-hosted runners.

## Commands

| Command | Purpose |
| --- | --- |
| `task ready` | Format, tidy, lint, and test. Run this before you open a PR |
| `task go:test` | Run the unit tests with the race detector |
| `task docker:build` | Build the container image |

`Taskfile.yml` includes shared tasks from the `.taskfiles` submodule. Add project-specific tasks to `Taskfile.yml`, and do not edit the submodule.

## Key directories

| Directory | Purpose |
| --- | --- |
| `cmd/notifications-relay` | Process start-up and wiring |
| `internal/config` | Configuration loading and validation |
| `internal/event` | The source-independent event type |
| `internal/route` | Route matching |
| `internal/relay` | HTTP server and fan-out to sinks |
| `internal/source/github` | GitHub signature checks and payload parsing |
| `internal/sink/discord` | Discord message format and delivery |

## Pull requests

PR titles follow Conventional Commits, because semantic-release sets the version from them. Pull requests are squash-merged.

When you add an event type or attribute, update the tables in `README.md`.
