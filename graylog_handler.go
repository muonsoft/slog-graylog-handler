package graylog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const StackTraceKey = "_stacktrace"

// BufSize is the buffer size for the async handler. Set graylog.BufSize = <value>
// before calling NewAsyncHandler. Once the buffer is full, logging will
// block until slots are available.
var BufSize uint = 8192

// HandlerOptions configures a Handler.
type HandlerOptions struct {
	Level     slog.Leveler
	Host      string
	Extra     map[string]interface{}
	Blacklist []string
	AddSource bool
}

// Handler sends log records to a Graylog server via GELF (UDP, TCP, or HTTP).
// It implements slog.Handler.
type Handler struct {
	opts        HandlerOptions
	host        string
	gelfLogger  GELFWriter
	attrs       []slog.Attr
	groups      []string
	mu          sync.RWMutex
	blacklist   map[string]bool
	buf         chan graylogRecord
	wg          *sync.WaitGroup
	synchronous bool
}

type graylogRecord struct {
	record slog.Record
	file   string
	line   int
	attrs  []slog.Attr
	groups []string
}

// NewHandler creates a synchronous handler that sends logs to Graylog.
func NewHandler(addr string, opts *HandlerOptions) (*Handler, error) {
	g, err := NewWriter(addr)
	if err != nil {
		return nil, fmt.Errorf("create GELF writer: %w", err)
	}
	host := resolveHost(opts)
	h := &Handler{
		opts:        defaultHandlerOptions(opts),
		host:        host,
		gelfLogger:  g,
		synchronous: true,
	}
	h.applyBlacklist()
	return h, nil
}

// NewAsyncHandler creates an asynchronous handler. Call Flush() before
// exiting to drain the log queue.
func NewAsyncHandler(addr string, opts *HandlerOptions) (*Handler, error) {
	g, err := NewWriter(addr)
	if err != nil {
		return nil, fmt.Errorf("create GELF writer: %w", err)
	}
	host := resolveHost(opts)
	h := &Handler{
		opts:        defaultHandlerOptions(opts),
		host:        host,
		gelfLogger:  g,
		buf:         make(chan graylogRecord, BufSize),
		wg:          &sync.WaitGroup{},
		synchronous: false,
	}
	h.applyBlacklist()
	go h.fire()
	return h, nil
}

func defaultHandlerOptions(opts *HandlerOptions) HandlerOptions {
	if opts == nil {
		return HandlerOptions{
			Level: slog.LevelDebug,
		}
	}
	if opts.Level == nil {
		opts.Level = slog.LevelDebug
	}
	if opts.Extra == nil {
		opts.Extra = make(map[string]interface{})
	}
	return *opts
}

func resolveHost(opts *HandlerOptions) string {
	if opts != nil && opts.Host != "" {
		return opts.Host
	}
	host, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return host
}

func (h *Handler) applyBlacklist() {
	h.blacklist = make(map[string]bool)
	for _, k := range h.opts.Blacklist {
		h.blacklist[k] = true
	}
}

// Enabled reports whether the handler processes records at the given level.
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return level >= h.opts.Level.Level()
}

// Handle processes a log record and sends it to Graylog.
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var file string
	var line int
	if h.opts.AddSource && record.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{record.PC})
		if f, _ := fs.Next(); f.File != "" {
			file = f.File
			line = f.Line
		}
	}

	gr := graylogRecord{
		record: record.Clone(),
		file:   file,
		line:   line,
		attrs:  cloneAttrs(h.attrs),
		groups: cloneStrings(h.groups),
	}
	if h.synchronous {
		h.sendRecord(gr)
	} else {
		h.wg.Add(1)
		h.buf <- gr
	}
	return nil
}

// WithAttrs returns a new handler with the given attributes added to every record.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.RLock()
	defer h.mu.RUnlock()
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	newAttrs = append(newAttrs, attrs...)
	return &Handler{
		opts:        h.opts,
		host:        h.host,
		gelfLogger:  h.gelfLogger,
		attrs:       newAttrs,
		groups:      h.groups,
		blacklist:   h.blacklist,
		buf:         h.buf,
		wg:          h.wg,
		synchronous: h.synchronous,
	}
}

// WithGroup returns a new handler with the given group name for attribute qualification.
func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	newGroups := make([]string, len(h.groups), len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups = append(newGroups, name)
	return &Handler{
		opts:        h.opts,
		host:        h.host,
		gelfLogger:  h.gelfLogger,
		attrs:       h.attrs,
		groups:      newGroups,
		blacklist:   h.blacklist,
		buf:         h.buf,
		wg:          h.wg,
		synchronous: h.synchronous,
	}
}

// Flush waits for the async log queue to drain. No-op for synchronous handler.
func (h *Handler) Flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.wg != nil {
		h.wg.Wait()
	}
}

func (h *Handler) fire() {
	for gr := range h.buf {
		h.sendRecord(gr)
		h.wg.Done()
	}
}

func slogLevelToSyslog(level slog.Level) int32 {
	const (
		LOG_EMERG   = 0
		LOG_ALERT   = 1
		LOG_CRIT    = 2
		LOG_ERR     = 3
		LOG_WARNING = 4
		LOG_NOTICE  = 5
		LOG_INFO    = 6
		LOG_DEBUG   = 7
	)
	switch {
	case level >= slog.LevelError:
		return LOG_ERR
	case level >= slog.LevelWarn:
		return LOG_WARNING
	case level >= slog.LevelInfo:
		return LOG_INFO
	default:
		return LOG_DEBUG
	}
}

func (h *Handler) sendRecord(gr graylogRecord) {
	if h.gelfLogger == nil {
		return
	}
	record := gr.record
	msg := record.Message
	p := bytes.TrimSpace([]byte(msg))
	short := p
	full := []byte("")
	if i := bytes.IndexRune(p, '\n'); i > 0 {
		short = p[:i]
		full = p
	}
	level := slogLevelToSyslog(record.Level)

	extra := make(map[string]interface{})
	for k, v := range h.opts.Extra {
		extra["_"+k] = v
	}
	for _, a := range gr.attrs {
		if a.Equal(slog.Attr{}) {
			continue
		}
		key := prefixWithGroups(a.Key, gr.groups)
		extra["_"+key] = a.Value.Any()
	}
	if gr.file != "" {
		extra["_file"] = gr.file
		extra["_line"] = gr.line
		if record.PC != 0 {
			fs := runtime.CallersFrames([]uintptr{record.PC})
			if f, _ := fs.Next(); f.Function != "" {
				extra["_function"] = f.Function
			}
		}
	}
	record.Attrs(func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		key := prefixWithGroups(a.Key, gr.groups)
		if h.blacklist[key] {
			return true
		}
		v := a.Value.Any()
		extraK := "_" + key
		if err, ok := v.(error); ok {
			if _, ok := v.(json.Marshaler); !ok {
				extra[extraK] = newMarshalableError(err)
			} else {
				extra[extraK] = v
			}
		} else {
			extra[extraK] = v
		}
		return true
	})

	m := Message{
		Version:  "1.1",
		Host:     h.host,
		Short:    string(short),
		Full:     string(full),
		TimeUnix: float64(time.Now().UnixNano()/1000000) / 1000.,
		Level:    level,
		File:     gr.file,
		Line:     gr.line,
		Extra:    extra,
	}
	if err := h.gelfLogger.WriteMessage(&m); err != nil {
		fmt.Fprintln(os.Stderr, "graylog:", err)
	}
}

func cloneAttrs(a []slog.Attr) []slog.Attr {
	if len(a) == 0 {
		return nil
	}
	out := make([]slog.Attr, len(a))
	copy(out, a)
	return out
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func prefixWithGroups(key string, groups []string) string {
	if len(groups) == 0 {
		return key
	}
	return strings.Join(groups, ".") + "." + key
}

// Blacklist sets keys that will be omitted from GELF extra fields.
func (h *Handler) Blacklist(b []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blacklist = make(map[string]bool)
	for _, k := range b {
		h.blacklist[k] = true
	}
}

// SetWriter sets the GELF writer (e.g. to change compression).
func (h *Handler) SetWriter(w *LowLevelProtocolWriter) error {
	if w == nil {
		return errors.New("writer can't be nil")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gelfLogger = w
	return nil
}

// Writer returns the current GELF writer.
func (h *Handler) Writer() GELFWriter {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.gelfLogger
}
