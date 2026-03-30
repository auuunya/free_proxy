# free_proxy

English documentation. For the Chinese version, see [README.zh.md](README.zh.md).

`free_proxy` is a small Go library and CLI for collecting public proxy lists, validating them concurrently, and exporting usable results as JSON or TXT files.

## Features

- Collect proxies from multiple public sources.
- Load source definitions from built-in defaults or a JSON config file.
- Configure fetch concurrency, validation concurrency, timeouts, and retry behavior from the CLI.
- Deduplicate and normalize records before validation.
- Validate HTTP, HTTPS, and SOCKS5 candidates concurrently with optional early-stop.
- Optionally enrich validated proxies with region metadata.
- Tolerate individual source failures or timeouts and continue with successful sources.
- Print per-source fetch and validation summaries at the end of a run.
- Run interactively or through a script-friendly CLI.

## Quick Start

```bash
go run ./cmd/main.go
```

Interactive mode starts automatically when no flags are passed.

Non-interactive usage:

```bash
go run ./cmd/main.go \
  --count 50 \
  --valid-count 20 \
  --timeout 15s \
  --fetch-concurrency 12 \
  --validate-concurrency 150 \
  --fetch-retries 4 \
  --fetch-retry-delay 300ms \
  --source-config ./sources.json \
  --unfiltered-format json \
  --valid-format txt \
  --output-dir ./dist \
  --check-region \
  --only-ip
```

If one upstream source times out or returns an error, the CLI logs that source failure and continues with proxies collected from the remaining healthy sources. The run only stops early when the top-level context is canceled (for example, Ctrl+C).

`--count` limits how many collected proxies move into validation. `--valid-count` stops validation once enough working proxies are found, then writes a deterministic, sorted result set.

## CLI Flags

| Flag                     | Default | Description                                                                                        |
| ------------------------ | ------- | -------------------------------------------------------------------------------------------------- |
| `--interactive`          | `false` | Run the interactive workflow. Interactive mode also starts automatically when no flags are passed. |
| `--count`                | `0`     | Limit how many collected proxies move into validation; `0` uses all collected proxies.             |
| `--valid-count`          | `0`     | Stop validation after finding this many usable proxies; `0` validates all candidates.              |
| `--unfiltered-format`    | `json`  | Output format for the unfiltered proxy list: `json` or `txt`.                                      |
| `--valid-format`         | `json`  | Output format for the validated proxy list: `json` or `txt`.                                       |
| `--output-dir`           | `.`     | Directory where output files are written.                                                          |
| `--check-region`         | `false` | Look up region metadata for validated proxies.                                                     |
| `--check-type-https`     | `true`  | Detect proxy protocol and HTTPS support during validation.                                         |
| `--only-ip`              | `false` | When `--valid-format=txt`, write only `ip:port` entries, one per line.                             |
| `--timeout`              | `20s`   | Request timeout for source fetches and region lookups.                                             |
| `--fetch-concurrency`    | `8`     | Maximum number of source fetch requests running at the same time.                                  |
| `--validate-concurrency` | `100`   | Maximum number of proxy validation checks running at the same time.                                |
| `--fetch-retries`        | `3`     | Maximum number of attempts for each source page fetch.                                             |
| `--fetch-retry-delay`    | `500ms` | Base backoff delay between fetch retries.                                                          |
| `--source-config`        | `""`    | Path to a JSON file that overrides the built-in source list.                                       |

## Source Config File

You can override the built-in source list with `--source-config <path>`. The file must contain a JSON array of source objects:

```json
[
  {
    "name": "custom_text_source",
    "url": "https://example.com/proxies/{page}",
    "pages": 2,
    "kind": "api_text",
    "default_type": "HTTP",
    "default_https": "Unchecked"
  }
]
```

Supported `kind` values are `api_text`, `api_json`, and `web`. For `web` sources, also set `web_parser` to one of `freeproxylist`, `proxy_list_plus`, `89ip`, or `sslproxies`.

## Project Layout

- `cmd/main.go`: CLI entrypoint.
- `service.go`: workflow orchestration.
- `network.go`: fetch, validation, SOCKS5, and region lookup logic.
- `parsers.go`: source-specific parsing helpers.
- `output.go`: file serialization.
- `metadata.go`: reusable metadata helpers for downstream integrations.

## Development

Recommended Go version: `1.25.0`

```bash
go test ./...
```

If every source fails or no unique proxies are collected, the program exits without producing proxy output files.
