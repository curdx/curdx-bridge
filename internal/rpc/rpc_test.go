package rpc

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── ReadState ──

func TestReadState_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	payload := `{"host":"127.0.0.1","port":1234,"token":"abc"}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := ReadState(path)
	if got == nil {
		t.Fatal("expected non-nil state")
	}
	if got["host"] != "127.0.0.1" {
		t.Errorf("host=%v", got["host"])
	}
	if got["port"].(float64) != 1234 {
		t.Errorf("port=%v", got["port"])
	}
}

func TestReadState_Missing(t *testing.T) {
	if got := ReadState(filepath.Join(t.TempDir(), "does-not-exist.json")); got != nil {
		t.Errorf("expected nil for missing file, got %v", got)
	}
}

func TestReadState_BadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	_ = os.WriteFile(path, []byte("{not json"), 0o600)
	if got := ReadState(path); got != nil {
		t.Errorf("expected nil for bad JSON, got %v", got)
	}
}

func TestReadState_JSONArray(t *testing.T) {
	// JSON is valid but top level is an array, not an object.
	path := filepath.Join(t.TempDir(), "array.json")
	_ = os.WriteFile(path, []byte(`[1, 2, 3]`), 0o600)
	if got := ReadState(path); got != nil {
		t.Errorf("expected nil for array root, got %v", got)
	}
}

// ── extractConnectionInfo ──

func TestExtractConnectionInfo_ConnectHostPreferred(t *testing.T) {
	st := map[string]any{
		"connect_host": "10.0.0.1",
		"host":         "ignored",
		"port":         float64(8080),
		"token":        "tok",
	}
	host, port, token, ok := extractConnectionInfo(st)
	if !ok || host != "10.0.0.1" || port != 8080 || token != "tok" {
		t.Errorf("got (%q, %d, %q, %v)", host, port, token, ok)
	}
}

func TestExtractConnectionInfo_FallbackToHost(t *testing.T) {
	st := map[string]any{
		"host":  "192.168.1.1",
		"port":  float64(8081),
		"token": "tok",
	}
	host, _, _, ok := extractConnectionInfo(st)
	if !ok || host != "192.168.1.1" {
		t.Errorf("got host=%q ok=%v", host, ok)
	}
}

func TestExtractConnectionInfo_EmptyConnectHostFallsBack(t *testing.T) {
	// connect_host present but empty should fall through to host.
	st := map[string]any{
		"connect_host": "",
		"host":         "192.168.1.2",
		"port":         float64(9000),
		"token":        "t",
	}
	host, _, _, ok := extractConnectionInfo(st)
	if !ok || host != "192.168.1.2" {
		t.Errorf("got host=%q ok=%v", host, ok)
	}
}

func TestExtractConnectionInfo_MissingPort(t *testing.T) {
	st := map[string]any{"host": "127.0.0.1", "token": "t"}
	if _, _, _, ok := extractConnectionInfo(st); ok {
		t.Error("expected ok=false for missing port")
	}
}

func TestExtractConnectionInfo_MissingToken(t *testing.T) {
	st := map[string]any{"host": "127.0.0.1", "port": float64(1)}
	if _, _, _, ok := extractConnectionInfo(st); ok {
		t.Error("expected ok=false for missing token")
	}
}

func TestExtractConnectionInfo_StringPortRejected(t *testing.T) {
	// JSON numbers parse as float64 — a string "1234" shouldn't silently
	// succeed (that would be an unexpected value coercion).
	st := map[string]any{"host": "127.0.0.1", "port": "1234", "token": "t"}
	if _, _, _, ok := extractConnectionInfo(st); ok {
		t.Error("expected ok=false for string port")
	}
}

// ── PingDaemon ──

// fakeDaemon starts a one-shot TCP listener on 127.0.0.1:0 that reads a
// single newline-terminated request and replies with respBody + "\n".
// Returns the chosen port and a function to close the listener.
func fakeDaemon(t *testing.T, respBody string, capture *string, mu *sync.Mutex) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		scanner := bufio.NewScanner(conn)
		if scanner.Scan() {
			if capture != nil {
				mu.Lock()
				*capture = scanner.Text()
				mu.Unlock()
			}
		}
		_, _ = conn.Write([]byte(respBody + "\n"))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	return port, func() { ln.Close() }
}

// writeStateFile writes a minimal daemon state file to a temp dir.
func writeStateFile(t *testing.T, port int, token string) string {
	t.Helper()
	state := map[string]any{
		"connect_host": "127.0.0.1",
		"host":         "127.0.0.1",
		"port":         port,
		"token":        token,
	}
	data, _ := json.Marshal(state)
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
	return path
}

func TestPingDaemon_NoStateFile(t *testing.T) {
	if PingDaemon("cxb-askd", 0.5, "/nonexistent/state.json") {
		t.Error("expected false for missing state")
	}
}

func TestPingDaemon_Success(t *testing.T) {
	var captured string
	var mu sync.Mutex
	port, stop := fakeDaemon(t, `{"type":"cxb-askd.pong","exit_code":0}`, &captured, &mu)
	defer stop()
	statePath := writeStateFile(t, port, "tok-xyz")

	ok := PingDaemon("cxb-askd", 2.0, statePath)
	if !ok {
		t.Fatal("expected PingDaemon to return true")
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(captured, `"cxb-askd.ping"`) {
		t.Errorf("expected ping type in request, got: %s", captured)
	}
	if !strings.Contains(captured, `"tok-xyz"`) {
		t.Errorf("expected token in request, got: %s", captured)
	}
}

func TestPingDaemon_ResponseTypeMismatch(t *testing.T) {
	port, stop := fakeDaemon(t, `{"type":"bogus.response","exit_code":0}`, nil, nil)
	defer stop()
	statePath := writeStateFile(t, port, "tok")
	if PingDaemon("cxb-askd", 2.0, statePath) {
		t.Error("expected false on wrong response type")
	}
}

func TestPingDaemon_NonZeroExitCode(t *testing.T) {
	port, stop := fakeDaemon(t, `{"type":"cxb-askd.pong","exit_code":7}`, nil, nil)
	defer stop()
	statePath := writeStateFile(t, port, "tok")
	if PingDaemon("cxb-askd", 2.0, statePath) {
		t.Error("expected false on non-zero exit_code")
	}
}

func TestPingDaemon_DialRefused(t *testing.T) {
	// State points at a port that isn't listening. Use a port we just
	// released to make the refusal deterministic.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	statePath := writeStateFile(t, port, "tok")
	if PingDaemon("cxb-askd", 0.3, statePath) {
		t.Error("expected false on refused connection")
	}
}

func TestPingDaemon_ResponseAcceptsGenericResponseType(t *testing.T) {
	// The accepted response types are `<prefix>.pong` OR `<prefix>.response`.
	// Pin that contract so a refactor doesn't drop one variant.
	port, stop := fakeDaemon(t, `{"type":"cxb-askd.response","exit_code":0}`, nil, nil)
	defer stop()
	statePath := writeStateFile(t, port, "tok")
	if !PingDaemon("cxb-askd", 2.0, statePath) {
		t.Error("expected true for generic response type")
	}
}

// ── ShutdownDaemon ──

func TestShutdownDaemon_SendsRequest(t *testing.T) {
	var captured string
	var mu sync.Mutex
	port, stop := fakeDaemon(t, `{"type":"cxb-askd.response","exit_code":0}`, &captured, &mu)
	defer stop()
	statePath := writeStateFile(t, port, "tok")

	if !ShutdownDaemon("cxb-askd", 2.0, statePath) {
		t.Fatal("expected true on successful shutdown")
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(captured, `"cxb-askd.shutdown"`) {
		t.Errorf("expected shutdown type in request, got: %s", captured)
	}
}

func TestShutdownDaemon_NoStateFile(t *testing.T) {
	if ShutdownDaemon("cxb-askd", 0.5, filepath.Join(t.TempDir(), "missing.json")) {
		t.Error("expected false for missing state")
	}
}

// ── Integration-style: real round-trip with port in text form ──

func TestPingDaemon_EndToEndWithConnectHost(t *testing.T) {
	// Confirms the http-style host:port join works and the response
	// parser tolerates extra trailing whitespace from bufio.Scanner.
	port, stop := fakeDaemon(t,
		`{"type":"cxb-askd.pong","exit_code":0}`+"\r", nil, nil)
	defer stop()

	state := map[string]any{
		"connect_host": "127.0.0.1",
		"host":         "127.0.0.1",
		"port":         float64(port),
		"token":        "tok",
	}
	data, _ := json.Marshal(state)
	path := filepath.Join(t.TempDir(), "state.json")
	_ = os.WriteFile(path, data, 0o600)

	if !PingDaemon("cxb-askd", 2.0, path) {
		t.Fatalf("expected ping on port %s to succeed", strconv.Itoa(port))
	}
}

// Sanity: verify that io.EOF from the fake daemon path doesn't panic.
func TestPingDaemon_EmptyResponse(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		_ = conn.Close() // close without writing
	}()
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	statePath := writeStateFile(t, port, "tok")
	if PingDaemon("cxb-askd", 1.0, statePath) {
		t.Error("expected false on empty response")
	}
}

// Ensure we don't leak goroutines in the fake daemon helper.
func TestFakeDaemonHelperIsWellBehaved(t *testing.T) {
	port, stop := fakeDaemon(t, `{"type":"cxb-askd.pong","exit_code":0}`, nil, nil)
	defer stop()

	conn, err := net.DialTimeout("tcp",
		"127.0.0.1:"+strconv.Itoa(port),
		time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte("{\"id\":\"probe\"}\n"))
	buf, _ := io.ReadAll(conn)
	if !strings.Contains(string(buf), "pong") {
		t.Errorf("expected pong in response, got %q", buf)
	}
}
