# apiprobe-mcp

An MCP (Model Context Protocol) server written in Go that lets AI clients such as OpenCode call, probe, and assert on HTTP APIs directly.

It exposes six tools over MCP (stdio or streamable HTTP):

| Tool | Purpose |
| --- | --- |
| `http_request` | Send an HTTP request and get back status, headers, body, size, and timing |
| `api_test` | Send a request and run pass/fail assertions against the response |
| `save_auth` | Save a reusable auth profile (bearer, basic, api_key) |
| `list_auth` | List saved auth profiles with redacted secrets |
| `delete_auth` | Delete a saved auth profile by name |
| `openapi_endpoints` | List operations from an OpenAPI/Swagger spec (URL or local file) |

## Requirements

- Go 1.26 or newer (to build)
- Docker (optional, to run as a container)

## Build and run

Run locally from source:

```bash
go build -o apiprobe-mcp .
./apiprobe-mcp                      # stdio transport (default)
./apiprobe-mcp -transport http      # streamable HTTP transport
```

Run with Docker:

```bash
docker compose up -d                # serves streamable HTTP on port 8931
```

## Configuration

Flags can also be set via environment variables (flags take precedence):

| Flag | Environment variable | Default | Description |
| --- | --- | --- | --- |
| `-transport` | `APIPROBE_TRANSPORT` | `stdio` | Transport: `stdio` or `http` |
| `-addr` | `APIPROBE_ADDR` | `:8931` | Listen address for the HTTP transport |
| `-path` | `APIPROBE_PATH` | `/mcp` | Endpoint path for the HTTP transport |

## MCP client setup

stdio (local process):

```json
{
  "mcpServers": {
    "apiprobe-mcp": {
      "command": "./apiprobe-mcp"
    }
  }
}
```

streamable HTTP:

```json
{
  "mcpServers": {
    "apiprobe-mcp": {
      "type": "streamable-http",
      "url": "http://localhost:8931/mcp"
    }
  }
}
```

## Tool reference

### `http_request`

Send an HTTP request and get back status, headers, body, and timing.

Parameters: `method` (required, enum GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS), `url` (required, absolute http(s) URL), `headers` (object), `query` (object), `body` (string), `timeout_ms` (default 10000, max 300000), `auth_profile` (string).

### `api_test`

Same request parameters as `http_request`, plus assertions:

- `expect_status`: expected HTTP status code, e.g. 200
- `expect_json`: expected JSON values keyed by dot-path, e.g. `{"data.items.0.id": 1}`
- `expect_contains`: substring that must appear in the response body
- `expect_header`: header names mapped to value substrings that must appear

Returns `passed` with a per-check breakdown (`check`, `expected`, `actual`, `passed`); when a check fails, a truncated body excerpt is included.

### `save_auth`

Save a named auth profile: `name` (required), `type` (required, enum `bearer`/`basic`/`api_key`), `token`, `header` (api_key only, default `X-API-Key`), `username`/`password` (basic), `applies_to` (URL prefix).

Profiles live in memory for the server session. If `applies_to` is set, the profile is auto-applied to requests whose URL starts with that prefix; otherwise it must be referenced by name via `auth_profile`.

Example:

```json
{
  "name": "prod",
  "type": "bearer",
  "token": "<token>",
  "applies_to": "https://api.example.com"
}
```

### `list_auth`

Returns saved profiles with secrets redacted (`count`, `profiles`).

### `delete_auth`

Delete a profile by `name`; returns `{"name": ..., "deleted": true/false}`.

### `openapi_endpoints`

List operations from an OpenAPI/Swagger spec: `source` (required, http(s) URL or local file path), `method_filter`, `path_filter`, `limit` (default 100, max 500).

Returns `source`, `title`, `version`, `count`, and endpoints with `method`, `path`, `summary`, `operation_id`, `base_url`, `full_url`, and `parameters`.

## Behavior notes

- URL scheme must be http or https.
- Response bodies are capped at 64 KiB (`truncated: true` indicates a partial body).
- Timeout is capped at 300000 ms; a zero or negative value falls back to 10000 ms.
- Auth profiles and secrets are stored in memory only; nothing is persisted to disk or logged.
- Secrets are redacted in tool output; URLs are redacted in error messages.

## Testing

```bash
go test ./...
```

## Project structure

- `main.go`: server bootstrap and flags
- `http.go`: `http_request` and `api_test` tools with assertions
- `auth.go`: in-memory auth profiles and `save_auth`/`list_auth`/`delete_auth`
- `openapi.go`: `openapi_endpoints` tool
- `server_test.go`: tests
