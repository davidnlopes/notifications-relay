# notifications-relay

A generic webhook relay in Go. It receives webhooks from sources, matches each resulting event against configured routes, and posts it to the sinks those routes name. It is open source and has no database. Anything that varies between users belongs in the configuration file, so do not write assumptions about a specific deployment, cluster, domain, or person into code or docs.

## How the parts fit together

A source verifies a delivery and turns it into source-independent events (`internal/event`). `internal/route` picks the sinks for an event, and `internal/relay` serves HTTP and fans each event out to those sinks. `cmd/notifications-relay` wires configured sources and sinks to the relay. `internal/config` loads and validates the configuration, and `internal/source/*` and `internal/sink/*` hold one package per source and sink type.

## What must stay true

- `internal/route` and `internal/relay` must not know about any specific source or sink. Source-specific logic stays in `internal/source/*` and sink-specific logic in `internal/sink/*`, so that adding a type never changes routing or fan-out.
- A source verifies a delivery's signature before it parses the payload, so that unauthenticated input never reaches a parser.
- Secrets never go in the configuration file. A secret field names the environment variable that holds the value.
- A sink URL that contains a token, such as a Discord webhook URL, is never logged or returned in an error.
- The project keeps one runtime dependency, for YAML. Discuss any new dependency before you add it.
- CI runs on GitHub-hosted runners. The repository is public, so do not use self-hosted runners.

## Working in the repository

`Taskfile.yml` includes shared tasks from the `.taskfiles` submodule. Add project tasks to `Taskfile.yml`, and do not edit the submodule.

`README.md` documents the event types and attributes that each source produces. Update its tables when you add or change one.

Pull requests are squash-merged, and semantic-release sets the version from the PR title, so the title must follow Conventional Commits.
