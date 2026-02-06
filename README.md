# Graylog Handler for [log/slog](https://pkg.go.dev/log/slog)

[![Go Reference](https://pkg.go.dev/badge/github.com/muonsoft/slog-graylog-handler.svg)](https://pkg.go.dev/github.com/muonsoft/slog-graylog-handler)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/muonsoft/slog-graylog-handler)
![GitHub release (latest by date)](https://img.shields.io/github/v/release/muonsoft/slog-graylog-handler)
![GitHub](https://img.shields.io/github/license/muonsoft/slog-graylog-handler)
[![tests](https://github.com/muonsoft/slog-graylog-handler/actions/workflows/tests.yml/badge.svg)](https://github.com/muonsoft/slog-graylog-handler/actions/workflows/tests.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/muonsoft/slog-graylog-handler)](https://goreportcard.com/report/github.com/muonsoft/slog-graylog-handler)

Use this handler to send your logs to a [Graylog](http://graylog2.org) server via the GELF protocol (UDP, TCP, or HTTP).

It implements `slog.Handler`, so you create a `slog.Logger` with it. All slog attributes are sent as additional GELF fields (prefixed with `_`).

This library is an adaptation of [logrus-graylog-hook](https://github.com/gemnasium/logrus-graylog-hook) for the standard [log/slog](https://pkg.go.dev/log/slog) logger (Go 1.21+).

## Why this library?

- **Zero dependencies** — only the Go standard library. No external GELF client: UDP/TCP/HTTP, compression and chunking are implemented in-package.
- **No lost messages on shutdown** — the async handler provides an explicit `Flush()` that blocks until the send queue is drained. Call `defer handler.Flush()` before exit and all logs are delivered. In [samber/slog-graylog](https://github.com/samber/slog-graylog), [issue #4](https://github.com/samber/slog-graylog/issues/4) describes messages being dropped when the program exits because writes are done in goroutines with no way to wait for completion.

## Requirements

- Go 1.21 or later (for `log/slog`)

## Usage

### Direct handler (logs only to Graylog)

Configure with a Graylog GELF address and optional options:

- Address: `host:port` (UDP), `tcp://host:port` (TCP), or `http(s)://host/path` (HTTP)
- Optional: extra fields, minimum level, blacklist, source (file/line)

```go
package main

import (
	"log"
	"log/slog"
	"github.com/muonsoft/slog-graylog-handler"
)

func main() {
	handler, err := graylog.NewHandler("<graylog_ip>:<graylog_port>", &graylog.HandlerOptions{
		Level: slog.LevelInfo,
		Extra: map[string]interface{}{"app": "myservice"},
	})
	if err != nil {
		log.Fatalf("graylog handler: %v", err)
	}
	logger := slog.New(handler)
	logger.Info("some logging message", "key", "value")
}
```

### Asynchronous handler

Use `NewAsyncHandler` for non-blocking sends. Call `Flush()` before exit to drain the queue.

```go
handler, err := graylog.NewAsyncHandler("<graylog_ip>:<graylog_port>", &graylog.HandlerOptions{
	Extra: map[string]interface{}{"this": "is logged every time"},
})
if err != nil {
	log.Fatalf("graylog handler: %v", err)
}
defer handler.Flush()
logger := slog.New(handler)
logger.Info("some logging message")
```

### Log to both stdout and Graylog (MultiHandler)

Use `MultiHandler` to fan out to several handlers (e.g. console and Graylog):

```go
import (
	"log"
	"log/slog"
	"os"
	"github.com/muonsoft/slog-graylog-handler"
)

graylogHandler, err := graylog.NewHandler("<graylog_ip>:<graylog_port>", nil)
if err != nil {
	log.Fatalf("graylog handler: %v", err)
}
textHandler := slog.NewTextHandler(os.Stdout, nil)
logger := slog.New(graylog.NewMultiHandler(textHandler, graylogHandler))
logger.Info("message")
```

### Options

- **Level**: minimum level (e.g. `slog.LevelInfo`); default is Debug.
- **Host**: hostname in GELF messages; default is `os.Hostname()`.
- **Extra**: map of fields added to every message.
- **Blacklist**: slice of attribute keys to omit from GELF.
- **AddSource**: add `_file`, `_line`, `_function` from `slog.Record.PC` when true.

### Log only to Graylog (no stdout)

Create a logger with only the Graylog handler (no MultiHandler, no default handler). All logs go to Graylog:

```go
handler, err := graylog.NewHandler(graylogAddr, &graylog.HandlerOptions{Extra: map[string]interface{}{}})
if err != nil {
	log.Fatalf("graylog handler: %v", err)
}
logger := slog.New(handler)
logger.Info("Log messages are sent to Graylog only")
```
