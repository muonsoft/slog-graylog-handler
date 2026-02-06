package graylog

import (
	"compress/flate"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

const (
	SyslogInfoLevel  = 6
	SyslogErrorLevel = 3
)

func TestWritingToUDP(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), &HandlerOptions{
		Host:      "testing.local",
		Extra:     map[string]interface{}{"foo": "bar"},
		Blacklist: []string{"filterMe"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	msgData := "test message\nsecond line"

	logger := slog.New(handler)
	logger.Info(msgData, "withField", "1", "filterMe", "1")

	msg, err := r.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %s", err)
	}

	if msg.Short != "test message" {
		t.Errorf("msg.Short: expected test message, got %s", msg.Short)
	}
	if msg.Full != msgData {
		t.Errorf("msg.Full: expected %s, got %s", msgData, msg.Full)
	}
	if msg.Level != SyslogInfoLevel {
		t.Errorf("msg.Level: expected %d, got %d", SyslogInfoLevel, msg.Level)
	}
	if msg.Host != "testing.local" {
		t.Errorf("Host: expected testing.local, got %s", msg.Host)
	}
	if len(msg.Extra) != 2 {
		t.Errorf("wrong number of extra fields (exp: 2, got %d) in %v", len(msg.Extra), msg.Extra)
	}
	if msg.File != "" {
		t.Errorf("msg.File: expected empty, got %s", msg.File)
	}
	if msg.Line != 0 {
		t.Errorf("msg.Line: expected 0, got %d", msg.Line)
	}

	extra := map[string]interface{}{"foo": "bar", "withField": "1"}
	for k, v := range extra {
		if msg.Extra["_"+k].(string) != v.(string) {
			t.Errorf("Expected extra '%s' to be %#v, got %#v", k, v, msg.Extra["_"+k])
		}
	}
}

func TestWritingToTCP(t *testing.T) {
	listener, err := NewTCPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewTCPReader: %s", err)
	}
	msgData := "test message\nsecond line"
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		reader := &Reader{conn: conn}
		msg, err := reader.ReadMessage()
		if err != nil {
			t.Errorf("ReadMessage: %s", err)
			return
		}
		if msg.Short != "test message" {
			t.Errorf("msg.Short: expected test message, got %s", msg.Short)
		}
		if msg.Full != msgData {
			t.Errorf("msg.Full: expected %s, got %s", msgData, msg.Full)
		}
		if msg.Level != SyslogInfoLevel {
			t.Errorf("msg.Level: expected %d, got %d", SyslogInfoLevel, msg.Level)
		}
		if msg.Host != "testing.local" {
			t.Errorf("Host: expected testing.local, got %s", msg.Host)
		}
		if len(msg.Extra) != 2 {
			t.Errorf("wrong number of extra fields (exp: 2, got %d)", len(msg.Extra))
		}
		extra := map[string]interface{}{"foo": "bar", "withField": "1"}
		for k, v := range extra {
			if msg.Extra["_"+k].(string) != v.(string) {
				t.Errorf("Expected extra '%s' to be %#v, got %#v", k, v, msg.Extra["_"+k])
			}
		}
	}()

	handler, err := NewHandler("tcp://"+listener.Addr().String(), &HandlerOptions{
		Host:      "testing.local",
		Extra:     map[string]interface{}{"foo": "bar"},
		Blacklist: []string{"filterMe"},
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	logger := slog.New(handler)
	logger.Info(msgData, "withField", "1", "filterMe", "1")
	wg.Wait()
}

func TestErrorLevelReporting(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), &HandlerOptions{Extra: map[string]interface{}{"foo": "bar"}})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	msgData := "test message\nsecond line"

	logger := slog.New(handler)
	logger.Error(msgData)

	msg, err := r.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %s", err)
	}
	if msg.Short != "test message" {
		t.Errorf("msg.Short: expected test message, got %s", msg.Short)
	}
	if msg.Full != msgData {
		t.Errorf("msg.Full: expected %s, got %s", msgData, msg.Full)
	}
	if msg.Level != SyslogErrorLevel {
		t.Errorf("msg.Level: expected %d, got %d", SyslogErrorLevel, msg.Level)
	}
}

func TestJSONErrorMarshalling(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	logger := slog.New(handler)
	logger.Info("Testing sample error", "error", errors.New("sample error"))

	msg, err := r.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %s", err)
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Errorf("Marshaling json: %s", err)
	}
	errSection := regexp.MustCompile(`"_error":"sample error"`)
	if !errSection.MatchString(string(encoded)) {
		t.Errorf("Expected error message to be encoded into message")
	}
}

func TestParallelLogging(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	asyncHandler, err := NewAsyncHandler(r.Addr(), nil)
	if err != nil {
		t.Fatalf("NewAsyncHandler: %v", err)
	}
	defer asyncHandler.Flush()

	multi := NewMultiHandler(handler, asyncHandler)
	logger := slog.New(multi)
	logger2 := slog.New(multi)

	quit := make(chan struct{})
	defer close(quit)

	recordPanic := func() {
		if r := recover(); r != nil {
			t.Fatalf("Logging in parallel caused a panic")
		}
	}

	var wg sync.WaitGroup
	go func() {
		defer recordPanic()
		for {
			select {
			case <-quit:
				return
			default:
				r.ReadMessage()
			}
		}
	}()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer recordPanic()
			logger.Info("Logging")
			logger2.Info("Logging from another logger")
		}()
	}
	wg.Wait()
}

func TestSetWriter(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	w := handler.Writer().(*LowLevelProtocolWriter)
	w.CompressionLevel = flate.BestCompression
	if err := handler.SetWriter(w); err != nil {
		t.Fatal(err)
	}
	if handler.Writer().(*LowLevelProtocolWriter).CompressionLevel != flate.BestCompression {
		t.Error("Writer was not set correctly")
	}
	if handler.SetWriter(nil) == nil {
		t.Error("Setting a nil writer should return an error")
	}
}

func TestWithInvalidGraylogAddr(t *testing.T) {
	addr, err := net.ResolveUDPAddr("udp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(addr.String(), nil)
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	logger := slog.New(handler)
	// Should not panic
	logger.Info("Testing sample error", "error", errors.New("sample error"))
}

func TestSlogLevelToSyslog(t *testing.T) {
	const (
		LOG_ERR     = 3
		LOG_WARNING = 4
		LOG_INFO    = 6
		LOG_DEBUG   = 7
	)
	if slogLevelToSyslog(slog.LevelDebug) != LOG_DEBUG {
		t.Error("slogLevelToSyslog(LevelDebug) != LOG_DEBUG")
	}
	if slogLevelToSyslog(slog.LevelInfo) != LOG_INFO {
		t.Error("slogLevelToSyslog(LevelInfo) != LOG_INFO")
	}
	if slogLevelToSyslog(slog.LevelWarn) != LOG_WARNING {
		t.Error("slogLevelToSyslog(LevelWarn) != LOG_WARNING")
	}
	if slogLevelToSyslog(slog.LevelError) != LOG_ERR {
		t.Error("slogLevelToSyslog(LevelError) != LOG_ERR")
	}
}

func TestReportCallerEnabled(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), &HandlerOptions{
		Host:      "testing.local",
		AddSource: true,
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	msgData := "test message\nsecond line"

	logger := slog.New(handler)
	logger.Info(msgData)

	msg, err := r.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %s", err)
	}
	fileField, ok := msg.Extra["_file"]
	if !ok {
		t.Error("_file field not present in extra fields")
	}
	fileGot, ok := fileField.(string)
	if !ok {
		t.Error("_file field is not a string")
	}
	fileExpected := "graylog_handler_test.go"
	if !strings.HasSuffix(fileGot, fileExpected) {
		t.Errorf("msg.Extra[_file]: expected suffix %s, got %s", fileExpected, fileGot)
	}
	_, ok = msg.Extra["_line"]
	if !ok {
		t.Error("_line field not present in extra fields")
	}
	functionField, ok := msg.Extra["_function"]
	if !ok {
		t.Error("_function field not present in extra fields")
	}
	functionGot, ok := functionField.(string)
	if !ok {
		t.Error("_function field is not a string")
	}
	functionExpected := "TestReportCallerEnabled"
	if !strings.HasSuffix(functionGot, functionExpected) {
		t.Errorf("msg.Extra[_function]: expected suffix %s, got %s", functionExpected, functionGot)
	}
	if !strings.HasSuffix(msg.File, fileExpected) {
		t.Errorf("msg.File: expected suffix %s, got %s", fileExpected, msg.File)
	}
}

func TestReportCallerDisabled(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	handler, err := NewHandler(r.Addr(), &HandlerOptions{Host: "testing.local"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	msgData := "test message\nsecond line"

	logger := slog.New(handler)
	logger.Info(msgData)

	msg, err := r.ReadMessage()
	if err != nil {
		t.Errorf("ReadMessage: %s", err)
	}
	if _, ok := msg.Extra["_file"]; ok {
		t.Error("_file field should not be present in extra fields")
	}
	if _, ok := msg.Extra["_line"]; ok {
		t.Error("_line field should not be present in extra fields")
	}
	if _, ok := msg.Extra["_function"]; ok {
		t.Error("_function field should not be present in extra fields")
	}
	if msg.File != "" {
		t.Errorf("msg.File: expected empty, got %s", msg.File)
	}
	if msg.Line != 0 {
		t.Errorf("msg.Line: expected 0, got %d", msg.Line)
	}
}

func TestHTTPWriter(t *testing.T) {
	var gelf map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		all, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal("Unable to read response body")
		}
		if err := json.Unmarshal(all, &gelf); err != nil {
			t.Fatal("Unable to unmarshal json")
		}
		if gelf["host"] != "testing.local" {
			t.Errorf("host: expected testing.local, got %s", gelf["host"])
		}
		rw.WriteHeader(202)
	}))
	defer server.Close()

	handler, err := NewHandler(server.URL, &HandlerOptions{Host: "testing.local"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	logger := slog.New(handler)
	logger.Info("test message\nsecond line")
}

func TestWithAttrs(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	h, err := NewHandler(r.Addr(), &HandlerOptions{Host: "testing.local"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	handler := h.WithAttrs([]slog.Attr{slog.String("global", "value")}).(*Handler)

	logger := slog.New(handler)
	logger.Info("msg", "k", "v")

	msg, err := r.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %s", err)
	}
	if msg.Extra["_global"].(string) != "value" {
		t.Errorf("_global: expected value, got %v", msg.Extra["_global"])
	}
	if msg.Extra["_k"].(string) != "v" {
		t.Errorf("_k: expected v, got %v", msg.Extra["_k"])
	}
}

func TestWithGroup(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	h, err := NewHandler(r.Addr(), &HandlerOptions{Host: "testing.local"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	handler := h.WithGroup("request").(*Handler)

	logger := slog.New(handler)
	logger.Info("msg", "id", "123")

	msg, err := r.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %s", err)
	}
	if msg.Extra["_request.id"].(string) != "123" {
		t.Errorf("_request.id: expected 123, got %v", msg.Extra["_request.id"])
	}
}

func TestMultiHandler(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatalf("NewUDPReader: %s", err)
	}
	graylogHandler, err := NewHandler(r.Addr(), &HandlerOptions{Host: "testing.local"})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	multi := NewMultiHandler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}), graylogHandler)
	logger := slog.New(multi)
	logger.Info("only in graylog")
	msg, err := r.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %s", err)
	}
	if msg.Short != "only in graylog" {
		t.Errorf("msg.Short: expected only in graylog, got %s", msg.Short)
	}
}

func TestReaderAddr(t *testing.T) {
	r, err := NewUDPReader("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := r.Addr()
	if addr == "" {
		t.Error("Addr() should not be empty")
	}
}
