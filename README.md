# free_proxy

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
    "default_https": "待检测"
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

Recommended Go version: `1.21.13`

```bash
go test ./...
```

If every source fails or no unique proxies are collected, the program exits without producing proxy output files.

If you use a version manager:

- `mise` / `asdf`: this repo now includes `.tool-versions`
- `goenv` / `gvm`: this repo now includes `.go-version`
- Go 1.21+ native toolchain selection: `go.mod` now suggests `toolchain go1.21.13`

## Suggestions Before Publishing

- Replace the `module free_proxy` path in `go.mod` with the final Git hosting path.
- Add a `LICENSE` file after deciding the repository's legal terms.
- Add CI for `go test ./...`, `gofmt`, and `go vet`.
- Introduce interfaces around remote sources and validators if you want stable offline tests.
- Consider making source lists and concurrency limits configurable through a config file.
