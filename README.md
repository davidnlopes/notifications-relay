# notifications-relay

`notifications-relay` receives webhooks, filters them with rules you configure, and posts the matching events to chat channels. It runs as one small container with no database.

The relay supports GitHub as a source and Discord as a destination. Sources and sinks are separate packages, so you can add others without changing how routing works.

The relay exists because webhook senders filter too little. GitHub can select the `pull_request` event, but it can't select "a review was requested from me, and send that to this channel". Routes in this project can.

## Run the relay

The relay needs a configuration file and one environment variable for each secret that the file names.

1. Copy `config.example.yaml` to `config.yaml`, and set `requested_reviewer` to your GitHub login.
2. Create one webhook per Discord channel in **Channel settings** > **Integrations** > **Webhooks**.
3. Generate the secret that GitHub signs deliveries with:

   ```bash
   openssl rand -hex 32
   ```

4. Start the container:

   ```bash
   docker run --rm -p 8080:8080 \
     -v "$PWD/config.yaml:/etc/notifications-relay/config.yaml:ro" \
     -e GITHUB_WEBHOOK_SECRET \
     -e DISCORD_WEBHOOK_REVIEW_REQUESTS \
     -e DISCORD_WEBHOOK_CI_FAILURES \
     -e DISCORD_WEBHOOK_PULL_REQUESTS \
     -e DISCORD_WEBHOOK_MERGES \
     -e DISCORD_WEBHOOK_RELEASES \
     ghcr.io/davidnlopes/notifications-relay:v1
   ```

To validate a configuration without starting the server, add `-check` to the end of the command.

The image runs as a non-root user and supports `linux/amd64` and `linux/arm64`. It works with a read-only root filesystem. The HTTP server does not terminate TLS, so put it behind a reverse proxy that does.

## Connect a GitHub webhook

GitHub must be able to reach the relay at a public HTTPS URL. To send events from every repository in an organization, use an organization webhook. For a single repository, use a repository webhook.

1. Open **Settings** > **Webhooks** > **Add webhook** on the organization or repository.
2. Set **Payload URL** to `https://YOUR_HOST/hooks/github`. The last path segment is the source `name` in your configuration.
3. Set **Content type** to `application/json`.
4. Set **Secret** to the value of `GITHUB_WEBHOOK_SECRET`.
5. Select these individual events:
   - **Pull requests**
   - **Releases**
   - **Workflow runs**

GitHub sends a `ping` event when you save the webhook. A `204` response in **Recent Deliveries** confirms that the relay accepted the signature.

## Configuration reference

The configuration file has four sections. For a complete file, see [`config.example.yaml`](config.example.yaml). The relay refuses to start if the file has an unknown field, a route names an undefined sink or source, or a secret variable is empty.

### Server settings

All server settings are optional:

| Field | Default | Purpose |
| --- | --- | --- |
| `listen` | `:8080` | Address that the HTTP server binds to |
| `max_body_bytes` | `1048576` | Largest request body. Larger requests receive `413` |
| `max_in_flight` | `16` | Most webhook requests handled at once. Further requests receive `503` |

### Sources and sinks

Each source and sink has a unique `name` and a `type`:

| Section | `type` | Secret field | Secret value |
| --- | --- | --- | --- |
| `sources` | `github` | `secret_env` | Webhook signing secret |
| `sinks` | `discord` | `webhook_url_env` | Discord webhook URL |

Secret fields hold the name of an environment variable, not the value.

### Routes

An event goes to the sinks of every route that matches it. Each sink receives an event once, even if many routes match.

| Field | Matches when | If empty |
| --- | --- | --- |
| `sources` | The event came from a listed source | Any source matches |
| `types` | The event type is listed | Any type matches |
| `where` | Every listed attribute has one of its values | Nothing is required |
| `unless` | No listed attribute has one of its values | Nothing is excluded |

For example, this route sends review requests for one person to one channel, and ignores requests that Dependabot raises:

```yaml
routes:
  - name: review-requests-for-me
    sources: [github]
    types: [pull_request.review_requested]
    where:
      requested_reviewer: [your-github-login]
    unless:
      sender: ["dependabot[bot]"]
    sinks: [review-requests]
```

## GitHub events and attributes

The GitHub source produces the event types in this table. It ignores all other deliveries and responds with `204`.

| Event type | Attributes added to the common set |
| --- | --- |
| `pull_request.opened` | `author`, `base_branch`, `draft` |
| `pull_request.reopened` | `author`, `base_branch`, `draft` |
| `pull_request.ready_for_review` | `author`, `base_branch`, `draft` |
| `pull_request.review_requested` | `author`, `base_branch`, `draft`, and `requested_reviewer` or `requested_team` |
| `pull_request.merged` | `author`, `base_branch`, `draft` |
| `pull_request.closed` | `author`, `base_branch`, `draft` |
| `release.published` | `tag`, `prerelease` |
| `workflow_run.completed` | `workflow`, `conclusion`, `trigger`, `branch`, `actor` |

Every event also has the common attributes `repository`, `private`, and `sender`. Boolean attributes have the values `"true"` and `"false"`.

A request to a team sets `requested_team` to the team slug, not `requested_reviewer`. To receive those requests, add a separate route for your team.

## HTTP API

The relay serves two endpoints:

| Endpoint | Purpose |
| --- | --- |
| `POST /hooks/{source}` | Receives a webhook delivery for the source with that name |
| `GET /healthz` | Returns `200` while the process is serving requests |

`POST /hooks/{source}` returns one of these statuses:

| Status | Meaning |
| --- | --- |
| `204` | The relay verified the delivery, and every matching sink accepted it |
| `400` | The signature is invalid or the payload can't be parsed |
| `404` | No source has that name |
| `413` | The body is larger than `max_body_bytes` |
| `502` | At least one sink failed. Check the logs, then redeliver the webhook |
| `503` | The relay is already handling `max_in_flight` requests |

## Delivery behaviour and limits

The relay stores nothing, which keeps it small but has these effects:

- **Failed deliveries:** If a sink fails, the sender sees `502`. The relay retries once if Discord asks it to wait 3 seconds or less. It does not queue events.
- **Outages:** Events sent while the relay is down are lost unless the sender can redeliver them. GitHub lists past deliveries in **Recent Deliveries**.
- **Partial failures:** If you redeliver after a `502`, sinks that already succeeded receive the event again.
- **Repeated events:** The relay does not group or remove duplicates. If a workflow fails 10 times, you get 10 messages.

## Security

- The GitHub source checks the HMAC-SHA256 signature before it parses any payload.
- Discord webhook URLs contain a token, and the relay removes them from error messages and logs.
- Discord messages disable all mentions, so a PR title can't ping `@everyone`.
- The body size limit and the in-flight limit together bound the memory that unauthenticated requests can use.

## Add a source or sink

A source implements `relay.Source`, and a sink implements `relay.Sink`. Both interfaces are in `internal/relay/relay.go`. To add one:

1. Add a package under `internal/source` or `internal/sink`.
2. Add its type to `internal/config/config.go`, with validation for its secret.
3. Construct it in `buildHandler` in `cmd/notifications-relay/main.go`.

## Develop the relay

Clone with submodules, because `Taskfile.yml` includes the shared tasks from `.taskfiles`. You also need [Task](https://taskfile.dev) 3.47 or later:

```bash
git clone --recurse-submodules https://github.com/davidnlopes/notifications-relay.git
```

To list every task, run `task --list`. These are the tasks you use most:

| Task | Purpose |
| --- | --- |
| `task ready` | Formats, tidies, lints, and tests the code |
| `task go:test` | Runs the unit tests with the race detector |
| `task go:build` | Builds `bin/notifications-relay` |
| `task docker:build` | Builds the container image |

## Releases

Merging to `main` runs semantic-release, which sets the version from the PR title. If it creates a release, the same workflow publishes these image tags to the GitHub Container Registry: `vX.Y.Z`, `vX.Y`, `vX`, and `latest`.

## License

[MIT](LICENSE)
