// Package rpc provides daemon RPC utilities: state reading, ping, shutdown.
// Source: claude_code_bridge/lib/askd_rpc.py
package rpc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// ReadState reads and parses a JSON state file. Returns nil on any error.
func ReadState(stateFile string) map[string]interface{} {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return nil
	}
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil
	}
	m, ok := obj.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

// PingDaemon sends a ping to the daemon described by the state file and
// returns true if the daemon responds with a pong/response with exit_code 0.
func PingDaemon(protocolPrefix string, timeoutS float64, stateFile string) bool {
	st := ReadState(stateFile)
	if st == nil {
		return false
	}

	host, port, token, ok := extractConnectionInfo(st)
	if !ok {
		return false
	}

	timeout := time.Duration(timeoutS * float64(time.Second))

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	req := map[string]interface{}{
		"type":  protocolPrefix + ".ping",
		"v":     1,
		"id":    "ping",
		"token": token,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return false
	}
	reqBytes = append(reqBytes, '\n')

	conn.SetDeadline(time.Now().Add(timeout))
	_, err = conn.Write(reqBytes)
	if err != nil {
		return false
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return false
	}
	line := scanner.Text()

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return false
	}

	respType, _ := resp["type"].(string)
	if respType != protocolPrefix+".pong" && respType != protocolPrefix+".response" {
		return false
	}

	// Check exit_code == 0
	exitCode := 0
	if ec, ok := resp["exit_code"]; ok && ec != nil {
		switch v := ec.(type) {
		case float64:
			exitCode = int(v)
		case string:
			// Python: int(resp.get("exit_code") or 0)
			// A string "0" would parse, but we keep it simple
			if v != "0" && v != "" {
				return false
			}
		}
	}
	return exitCode == 0
}

// ShutdownDaemon sends a shutdown request to the daemon and returns true on success.
func ShutdownDaemon(protocolPrefix string, timeoutS float64, stateFile string) bool {
	st := ReadState(stateFile)
	if st == nil {
		return false
	}

	host, port, token, ok := extractConnectionInfo(st)
	if !ok {
		return false
	}

	timeout := time.Duration(timeoutS * float64(time.Second))

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)), timeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	req := map[string]interface{}{
		"type":  protocolPrefix + ".shutdown",
		"v":     1,
		"id":    "shutdown",
		"token": token,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return false
	}
	reqBytes = append(reqBytes, '\n')

	conn.SetDeadline(time.Now().Add(timeout))
	_, err = conn.Write(reqBytes)
	if err != nil {
		return false
	}

	// Read response (discard)
	buf := make([]byte, 1024)
	conn.Read(buf)

	return true
}

// extractConnectionInfo pulls host, port, and token from the state dict.
func extractConnectionInfo(st map[string]interface{}) (host string, port int, token string, ok bool) {
	// host: prefer connect_host, fall back to host
	if h, exists := st["connect_host"]; exists {
		host, _ = h.(string)
	}
	if host == "" {
		h, exists := st["host"]
		if !exists {
			return "", 0, "", false
		}
		host, _ = h.(string)
		if host == "" {
			return "", 0, "", false
		}
	}

	// port
	portVal, exists := st["port"]
	if !exists {
		return "", 0, "", false
	}
	switch v := portVal.(type) {
	case float64:
		port = int(v)
	default:
		return "", 0, "", false
	}

	// token
	tokenVal, exists := st["token"]
	if !exists {
		return "", 0, "", false
	}
	token, _ = tokenVal.(string)
	if token == "" {
		return "", 0, "", false
	}

	return host, port, token, true
}
