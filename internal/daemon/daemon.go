// Package daemon provides the unified ask daemon.
// Source: claude_code_bridge/lib/askd/daemon.py
package daemon

import (
	"fmt"
	"sync"
	"time"

	"github.com/curdx/curdx-bridge/internal/adapter"
	"github.com/curdx/curdx-bridge/internal/protocol"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/registry"
	"github.com/curdx/curdx-bridge/internal/runtime"
	"github.com/curdx/curdx-bridge/internal/server"
	"github.com/curdx/curdx-bridge/internal/workerpool"
)

// AskdSpec is the daemon spec for the unified ask daemon.
var AskdSpec = providers.ProviderDaemonSpec{
	DaemonKey:      "cxb-askd",
	ProtocolPrefix: "cxb-ask",
	StateFileName:  "cxb-askd.json",
	LogFileName:    "cxb-askd.log",
	IdleTimeoutEnv: "CURDX_ASKD_IDLE_TIMEOUT_S",
	LockName:       "cxb-askd",
}

func nowMs() int64 {
	return time.Now().UnixMilli()
}

func writeLog(line string) {
	runtime.WriteLog(runtime.LogPath(AskdSpec.LogFileName), line)
}

// ── Session Worker ──

// sessionWorkerAdapter wraps adapter.BaseProviderAdapter for workerpool.TaskHandler.
type sessionWorkerAdapter struct {
	adapterInst adapter.BaseProviderAdapter
	sessionKey  string
}

func newSessionWorker(sessionKey string, a adapter.BaseProviderAdapter) *workerpool.BaseSessionWorker {
	swa := &sessionWorkerAdapter{
		adapterInst: a,
		sessionKey:  sessionKey,
	}
	return workerpool.NewBaseSessionWorker(
		sessionKey,
		swa.handleTask,
		swa.handleError,
	)
}

func (s *sessionWorkerAdapter) handleTask(task workerpool.Task) (any, error) {
	qt, ok := task.(*queuedTaskAdapter)
	if !ok {
		return nil, fmt.Errorf("unexpected task type")
	}
	result := s.adapterInst.HandleTask(qt.inner)
	return result, nil
}

func (s *sessionWorkerAdapter) handleError(err error, task workerpool.Task) any {
	qt, ok := task.(*queuedTaskAdapter)
	if !ok {
		return adapter.DefaultHandleException(s.adapterInst.Key(), err, nil)
	}
	writeLog(fmt.Sprintf("[ERROR] provider=%s session=%s req_id=%s %v",
		s.adapterInst.Key(), s.sessionKey, qt.inner.ReqID, err))
	return s.adapterInst.HandleException(err, qt.inner)
}

// ── Task Adapter (bridges adapter.QueuedTask to workerpool.Task) ──

type queuedTaskAdapter struct {
	inner  *adapter.QueuedTask
	result any
}

func (q *queuedTaskAdapter) ReqID() string {
	return q.inner.ReqID
}

func (q *queuedTaskAdapter) IsCancelled() bool {
	return q.inner.IsCancelled()
}

func (q *queuedTaskAdapter) SetResult(value any) {
	q.result = value
	if r, ok := value.(*adapter.ProviderResult); ok {
		q.inner.SetResult(r)
	}
}

func (q *queuedTaskAdapter) Signal() {
	// SetResult already closes DoneEvent via inner.SetResult.
	// If no result was set, ensure we still signal.
	q.inner.SignalDone()
}

// ── Unified Worker Pool ──

type unifiedWorkerPool struct {
	registry *registry.ProviderRegistry
	pools    map[string]*workerpool.PerSessionWorkerPool
	mu       sync.Mutex
}

func newUnifiedWorkerPool(reg *registry.ProviderRegistry) *unifiedWorkerPool {
	return &unifiedWorkerPool{
		registry: reg,
		pools:    make(map[string]*workerpool.PerSessionWorkerPool),
	}
}

func (p *unifiedWorkerPool) getPool(providerKey string) *workerpool.PerSessionWorkerPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	pool, ok := p.pools[providerKey]
	if !ok {
		pool = workerpool.NewPerSessionWorkerPool()
		p.pools[providerKey] = pool
	}
	return pool
}

func (p *unifiedWorkerPool) submit(poolKey string, request adapter.ProviderRequest) *adapter.QueuedTask {
	baseProvider, instance := providers.ParseQualifiedProvider(poolKey)
	a := p.registry.Get(baseProvider)
	if a == nil {
		return nil
	}

	reqID := request.ReqID
	if reqID == "" {
		reqID = protocol.MakeReqID()
	}

	task := &adapter.QueuedTask{
		Request:     request,
		CreatedMs:   nowMs(),
		ReqID:       reqID,
		DoneEvent:   make(chan struct{}),
		CancelEvent: make(chan struct{}),
	}

	// Load session to compute routing key
	sess, _ := a.LoadSession(request.WorkDir, instance)
	sessionKey := ""
	if sess != nil {
		sessionKey = a.ComputeSessionKey(sess, instance)
	}
	if sessionKey == "" {
		sessionKey = poolKey + ":unknown"
	}

	pool := p.getPool(poolKey)
	worker := pool.GetOrCreate(sessionKey, func(sk string) *workerpool.BaseSessionWorker {
		return newSessionWorker(sk, a)
	})

	wrapped := &queuedTaskAdapter{inner: task}
	worker.Enqueue(wrapped)

	return task
}

// ── UnifiedAskDaemon ──

// UnifiedAskDaemon is the main daemon handling all providers.
type UnifiedAskDaemon struct {
	Registry *registry.ProviderRegistry
	Host     string
	Port     int
	WorkDir  string

	stateFile string
	pool      *unifiedWorkerPool
	mu        sync.Mutex
}

// NewUnifiedAskDaemon creates a new unified daemon.
func NewUnifiedAskDaemon(host string, port int, stateFile string, reg *registry.ProviderRegistry, workDir string) *UnifiedAskDaemon {
	if stateFile == "" {
		stateFile = runtime.StateFilePath(AskdSpec.StateFileName)
	}
	return &UnifiedAskDaemon{
		Registry:  reg,
		Host:      host,
		Port:      port,
		WorkDir:   workDir,
		stateFile: stateFile,
		pool:      newUnifiedWorkerPool(reg),
	}
}

// ServeForever starts the daemon and blocks.
func (d *UnifiedAskDaemon) ServeForever() int {
	d.Registry.StartAll()
	defer d.Registry.StopAll()

	token := runtime.RandomToken()
	srv := server.NewAskDaemonServer(AskdSpec, d.Host, d.Port, token, d.stateFile, d.handleRequest)
	srv.WorkDir = d.WorkDir
	srv.OnStop = func() {
		d.Registry.StopAll()
	}

	writeLog(fmt.Sprintf("daemon starting host=%s port=%d providers=%v", d.Host, d.Port, d.Registry.Keys()))

	if err := srv.ServeForever(); err != nil {
		writeLog(fmt.Sprintf("daemon error: %v", err))
		return 1
	}
	return 0
}

func (d *UnifiedAskDaemon) handleRequest(req map[string]any) map[string]any {
	providerName, _ := req["provider"].(string)
	if providerName == "" {
		return map[string]any{
			"type":      "cxb-ask.response",
			"v":         1,
			"id":        req["id"],
			"exit_code": 1,
			"reply":     "Missing 'provider' field",
		}
	}

	baseProvider, instance := providers.ParseQualifiedProvider(providerName)

	a := d.Registry.Get(baseProvider)
	if a == nil {
		return map[string]any{
			"type":      "cxb-ask.response",
			"v":         1,
			"id":        req["id"],
			"exit_code": 1,
			"reply":     fmt.Sprintf("Unknown provider: %s", baseProvider),
		}
	}

	caller, _ := req["caller"].(string)
	if caller == "" {
		return map[string]any{
			"type":      "cxb-ask.response",
			"v":         1,
			"id":        req["id"],
			"exit_code": 1,
			"reply":     "Missing 'caller' field (required).",
		}
	}

	// Build ProviderRequest
	clientID, _ := req["id"].(string)
	workDir, _ := req["work_dir"].(string)
	timeoutS := 300.0
	if v, ok := req["timeout_s"].(float64); ok {
		timeoutS = v
	}
	quiet, _ := req["quiet"].(bool)
	message, _ := req["message"].(string)
	outputPath, _ := req["output_path"].(string)
	reqID, _ := req["req_id"].(string)
	noWrap, _ := req["no_wrap"].(bool)
	emailReqID, _ := req["email_req_id"].(string)
	emailMsgID, _ := req["email_msg_id"].(string)
	emailFrom, _ := req["email_from"].(string)
	callerPaneID, _ := req["caller_pane_id"].(string)
	callerTerminal, _ := req["caller_terminal"].(string)

	provRequest := adapter.ProviderRequest{
		ClientID:       clientID,
		WorkDir:        workDir,
		TimeoutS:       timeoutS,
		Quiet:          quiet,
		Message:        message,
		Caller:         caller,
		OutputPath:     outputPath,
		ReqID:          reqID,
		NoWrap:         noWrap,
		EmailReqID:     emailReqID,
		EmailMsgID:     emailMsgID,
		EmailFrom:      emailFrom,
		Instance:       instance,
		CallerPaneID:   callerPaneID,
		CallerTerminal: callerTerminal,
	}

	poolKey := providers.MakeQualifiedKey(baseProvider, instance)
	task := d.pool.submit(poolKey, provRequest)
	if task == nil {
		return map[string]any{
			"type":      "cxb-ask.response",
			"v":         1,
			"id":        req["id"],
			"exit_code": 1,
			"reply":     fmt.Sprintf("Failed to submit task for provider: %s", providerName),
		}
	}

	// Wait for result with timeout
	var waitTimeout time.Duration
	if timeoutS < 0 {
		// Infinite wait - use a very large timeout
		waitTimeout = 24 * time.Hour
	} else {
		waitTimeout = time.Duration((timeoutS + 5.0) * float64(time.Second))
	}

	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()

	select {
	case <-task.DoneEvent:
		// Task completed
	case <-timer.C:
		// Timeout
		writeLog(fmt.Sprintf("[WARN] Task timeout, marking as cancelled: provider=%s req_id=%s", providerName, task.ReqID))
		task.Cancel()
	}

	result := task.GetResult()

	if result == nil {
		return map[string]any{
			"type":      "cxb-ask.response",
			"v":         1,
			"id":        clientID,
			"exit_code": 2,
			"reply":     "",
		}
	}

	resp := map[string]any{
		"type":      "cxb-ask.response",
		"v":         1,
		"id":        clientID,
		"req_id":    result.ReqID,
		"exit_code": result.ExitCode,
		"reply":     result.Reply,
		"provider":  providerName,
		"meta": map[string]any{
			"session_key":   result.SessionKey,
			"status":        result.Status,
			"done_seen":     result.DoneSeen,
			"done_ms":       result.DoneMs,
			"anchor_seen":   result.AnchorSeen,
			"anchor_ms":     result.AnchorMs,
			"fallback_scan": result.FallbackScan,
			"log_path":      result.LogPath,
		},
	}
	return resp
}
