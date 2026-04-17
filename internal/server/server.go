// Package server provides the TCP JSON-RPC daemon server.
// Source: claude_code_bridge/lib/askd_server.py
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/sessionutil"
)

// RequestHandler processes a JSON request and returns a JSON response.
type RequestHandler func(map[string]any) map[string]any

// AskDaemonServer is a TCP JSON-RPC server for the ask daemon.
type AskDaemonServer struct {
	Spec           providers.ProviderDaemonSpec
	Host           string
	Port           int
	Token          string
	StateFile      string
	RequestHandler RequestHandler
	OnStop         func()
	ParentPID      int
	Managed        bool
	WorkDir        string

	listener net.Listener
	mu       sync.Mutex
	stopped  bool
	stopCh   chan struct{}
}

// NewAskDaemonServer creates a new daemon server.
func NewAskDaemonServer(spec providers.ProviderDaemonSpec, host string, port int, token, stateFile string, handler RequestHandler) *AskDaemonServer {
	if host == "" {
		host = "127.0.0.1"
	}
	if token == "" {
		token = runtime.RandomToken()
	}
	if stateFile == "" {
		stateFile = runtime.StateFilePath(spec.StateFileName)
	}
	return &AskDaemonServer{
		Spec:           spec,
		Host:           host,
		Port:           port,
		Token:          token,
		StateFile:      stateFile,
		RequestHandler: handler,
		stopCh:         make(chan struct{}),
	}
}

// ServeForever starts the server and blocks until stopped.
func (s *AskDaemonServer) ServeForever() error {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	s.listener = ln

	// Get actual port if 0 was specified
	tcpAddr := ln.Addr().(*net.TCPAddr)
	s.Port = tcpAddr.Port

	// Write state file
	s.writeState()

	// Start heartbeat / parent PID monitor
	go s.monitorLoop()

	// Accept connections
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-s.stopCh:
				return nil
			default:
				continue
			}
		}
		go s.handleConn(conn)
	}
}

func (s *AskDaemonServer) handleConn(conn net.Conn) {
	defer conn.Close()
	// Initial deadline for reading the request line (short).
	conn.SetDeadline(time.Now().Add(30 * time.Second))

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	if !scanner.Scan() {
		return
	}
	line := scanner.Text()

	var req map[string]any
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return
	}

	// Verify token
	token, _ := req["token"].(string)
	if token != s.Token {
		return
	}

	reqType, _ := req["type"].(string)

	// Handle ping
	if strings.HasSuffix(reqType, ".ping") {
		resp := map[string]any{"type": strings.Replace(reqType, ".ping", ".pong", 1), "ok": true}
		data, _ := json.Marshal(resp)
		conn.Write(append(data, '\n'))
		return
	}

	// Handle shutdown
	if strings.HasSuffix(reqType, ".shutdown") {
		resp := map[string]any{"type": strings.Replace(reqType, ".shutdown", ".shutdown_ack", 1), "ok": true}
		data, _ := json.Marshal(resp)
		conn.Write(append(data, '\n'))
		go s.Stop()
		return
	}

	// Handle request — extend deadline based on request timeout_s
	if s.RequestHandler != nil {
		connTimeout := 5 * time.Minute
		if ts, ok := req["timeout_s"].(float64); ok && ts > 0 {
			connTimeout = time.Duration((ts+30)*float64(time.Second))
		}
		conn.SetDeadline(time.Now().Add(connTimeout))
		resp := s.RequestHandler(req)
		data, _ := json.Marshal(resp)
		conn.Write(append(data, '\n'))
	}
}

func (s *AskDaemonServer) writeState() {
	state := map[string]any{
		"host":         s.Host,
		"connect_host": runtime.NormalizeConnectHost(s.Host),
		"port":         s.Port,
		"token":        s.Token,
		"pid":          os.Getpid(),
	}
	if s.WorkDir != "" {
		state["work_dir"] = s.WorkDir
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	// Create the run dir with owner-only perms if it doesn't exist; avoids
	// the case where a later write side-effect creates the dir with 0o755
	// and auxiliary files then land there with default umask.
	_ = runtime.EnsureRunDir()
	sessionutil.SafeWriteSession(s.StateFile, string(data))
	// Belt and suspenders: the token is embedded in the JSON payload, so
	// enforce 0o600 explicitly after the atomic write. SafeWriteSession
	// already writes the tmp file at 0o600 and rename preserves that on
	// Unix, but an overzealous umask or a weird filesystem could still
	// leave the file world-readable. Chmod is a no-op on Windows.
	_ = os.Chmod(s.StateFile, 0o600)
}

func (s *AskDaemonServer) monitorLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			if s.ParentPID > 0 && !isPIDAlive(s.ParentPID) {
				s.Stop()
				return
			}
		}
	}
}

// Stop gracefully stops the server.
func (s *AskDaemonServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
	// Remove state file
	os.Remove(s.StateFile)
	if s.OnStop != nil {
		s.OnStop()
	}
}
