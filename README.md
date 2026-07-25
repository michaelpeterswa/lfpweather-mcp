# lfpweather-mcp

An [MCP](https://modelcontextprotocol.io) server that exposes the Lake Forest
Park home weather and environment station to LLM clients. It is a thin,
read-only layer over [`lfpweather-api`](https://github.com/michaelpeterswa/lfpweather-api):
the server calls the API's structured query and field-discovery endpoints and
surfaces them as MCP tools.

## What it serves

- **Weather** — temperature, humidity, barometric pressure, wind speed/gust,
  rainfall/rain rate, solar radiation, UV index.
- **Air quality** — AQI, CO2, NOx, TVOC.
- **Bird detections** — acoustic BirdNET species detections.
- **Off-grid power** — a LiTime battery and a Renogy solar charge controller
  (every numeric column is queryable).

History is available over ranges up to multiple years; time buckets are chosen
automatically to fit the requested range.

## Tools

| Tool | Purpose |
|------|---------|
| `query_weather` | Time series for one metric over a named range (`12h`…`all`, `ytd`, `prev_year`, `last_12mo`) or absolute `start`/`end`. Gauges return `avg/min/max/count/first/last`; `birdnet` returns per-bucket counts (optionally grouped by species). Accepts a friendly alias or a `table.column` reference. |
| `list_weather_fields` | Discovery: every queryable table, column, type, and the friendly aliases. |
| `get_weather_latest` | Most recent reading for one or more metrics (current conditions). |
| `get_bird_detections` | Species detection totals over a range, most-detected first (default 24h). |
| `get_current_time` | Current US Pacific time — the timezone the station reports in. |

All tools are read-only and non-destructive.

## Configuration

Configuration is via environment variables.

| Variable | Default | Description |
|----------|---------|-------------|
| `LFPWEATHER_API_BASE_URL` | `http://localhost:8080` | Base URL of `lfpweather-api` (no trailing path). |
| `LFPWEATHER_API_KEY` | — | Sent as `X-API-Key`; leave empty if the API has auth disabled. |
| `LFPWEATHER_API_TIMEOUT` | `10s` | Per-request timeout to the upstream API. |
| `TRANSPORT` | `streamablehttp` | `stdio`, `streamablehttp`, or `sse`. |
| `HTTP_PORT` | `8080` | Listen port for the HTTP transports. |
| `HTTP_PATH` | `/mcp` | Path the MCP endpoint is served on. |
| `MCP_AUTH_TOKEN` | — | If set, requires `Authorization: Bearer <token>`. Health probes are exempt. |
| `MCP_STATELESS` | `false` | Disable session tracking for load-balanced replicas. |
| `MCP_HEARTBEAT_INTERVAL` | `30s` | Keep-alive interval for idle SSE streams. |
| `SHUTDOWN_TIMEOUT` | `15s` | Grace period to drain in-flight requests on SIGTERM. |
| `SERVER_NAME` | `lfpweather-mcp` | Server name reported to clients. |
| `LOG_LEVEL` | `error` | `debug`, `info`, `warn`, or `error`. Logs are JSON on stderr. |
| `METRICS_ENABLED` / `METRICS_PORT` | `false` / `8081` | Prometheus metrics. |
| `TRACING_ENABLED` / `TRACING_SAMPLERATE` / `TRACING_SERVICE` / `TRACING_VERSION` | `false` / `0.01` / `lfpweather-mcp` / — | OpenTelemetry tracing. |

## Transports

- **`streamablehttp`** (default) — for hosting the server remotely. Clients
  connect to `http://<host>:<HTTP_PORT><HTTP_PATH>`.
- **`stdio`** — for local clients that launch the binary themselves (Claude
  Desktop, the MCP Inspector).
- **`sse`** — deprecated; kept for clients that have not migrated.

`/healthz` and `/readyz` are always served unauthenticated for probes.

## Running locally

```sh
LFPWEATHER_API_BASE_URL=http://localhost:8080 \
TRANSPORT=stdio \
go run ./cmd/lfpweather-mcp
```

Or with the MCP Inspector against the HTTP transport:

```sh
LFPWEATHER_API_BASE_URL=http://localhost:8080 go run ./cmd/lfpweather-mcp
# then point the inspector at http://localhost:8080/mcp
```

`docker-compose.yml` runs the server alongside a `grafana/otel-lgtm`
observability stack; set `LFPWEATHER_API_BASE_URL` to wherever the API runs.
