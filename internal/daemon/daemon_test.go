package daemon

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/curdx/curdx-bridge/internal/adapter"
	"github.com/curdx/curdx-bridge/internal/providers"
	"github.com/curdx/curdx-bridge/internal/registry"
)

// ── stub adapter ──
//
// stubAdapter implements adapter.BaseProviderAdapter with configurable
// behaviour so handleRequest can be exercised end-to-end without spawning
// real provider daemons.

type stubAdapter struct {
	key          string
	handleFunc   func(*adapter.QueuedTask) *adapter.ProviderResult
	handleCalled int
	sessionKey   string
	mu           sync.Mutex
	onStartCount int
	onStopCount  int
}

func (s *stubAdapter) Key() string { return s.key }
func (s *stubAdapter) Spec() providers.ProviderDaemonSpec {
	return providers.ProviderDaemonSpec{
		DaemonKey:      "cxb-" + s.key + "-askd",
		ProtocolPrefix: "cxb-" + s.key + "-ask",
		StateFileName:  "cxb-" + s.key + "-askd.json",
		LockName:       "cxb-" + s.key + "-askd",
	}
}
func (s *stubAdapter) SessionFilename() string { return "." + s.key + "-session" }

func (s *stubAdapter) LoadSession(workDir, instance string) (any, error) {
	// Return a non-nil value so the daemon treats it as a valid session.
	return map[string]string{"work_dir": workDir, "instance": instance}, nil
}

func (s *stubAdapter) ComputeSessionKey(session any, instance string) string {
	if s.sessionKey != "" {
		return s.sessionKey
	}
	return s.key + ":default"
}

func (s *stubAdapter) HandleTask(task *adapter.QueuedTask) *adapter.ProviderResult {
	s.mu.Lock()
	s.handleCalled++
	s.mu.Unlock()
	if s.handleFunc != nil {
		return s.handleFunc(task)
	}
	return &adapter.ProviderResult{
		ExitCode:   0,
		Reply:      "stub-reply-for:" + task.Request.Message,
		ReqID:      task.ReqID,
		SessionKey: s.key + ":default",
		Status:     "completed",
		DoneSeen:   true,
	}
}

func (s *stubAdapter) HandleException(err error, task *adapter.QueuedTask) *adapter.ProviderResult {
	return adapter.DefaultHandleException(s.key, err, task)
}

func (s *stubAdapter) OnStart() {
	s.mu.Lock()
	s.onStartCount++
	s.mu.Unlock()
}
func (s *stubAdapter) OnStop() {
	s.mu.Lock()
	s.onStopCount++
	s.mu.Unlock()
}

// newTestDaemon builds a UnifiedAskDaemon with one stub adapter registered.
// It never calls ServeForever, so no network is touched.
func newTestDaemon(t *testing.T, stub *stubAdapter) *UnifiedAskDaemon {
	t.Helper()
	reg := registry.New()
	reg.Register(stub)
	// stateFile must be non-empty so NewUnifiedAskDaemon doesn't try to
	// derive it via runtime.StateFilePath (which touches $HOME).
	d := NewUnifiedAskDaemon("127.0.0.1", 0, t.TempDir()+"/state.json", reg, "")
	return d
}

// ── handleRequest ──

func TestHandleRequest_MissingProvider(t *testing.T) {
	d := newTestDaemon(t, &stubAdapter{key: "codex"})
	resp := d.handleRequest(map[string]any{
		"id":     "req-1",
		"caller": "claude",
	})
	if resp["exit_code"] != 1 {
		t.Errorf("expected exit_code=1, got %v", resp["exit_code"])
	}
	reply, _ := resp["reply"].(string)
	if !strings.Contains(reply, "Missing 'provider'") {
		t.Errorf("expected Missing 'provider' reply, got %q", reply)
	}
	if resp["type"] != "cxb-ask.response" {
		t.Errorf("expected cxb-ask.response type, got %v", resp["type"])
	}
	if resp["id"] != "req-1" {
		t.Errorf("expected id to echo back, got %v", resp["id"])
	}
}

func TestHandleRequest_MissingCaller(t *testing.T) {
	d := newTestDaemon(t, &stubAdapter{key: "codex"})
	resp := d.handleRequest(map[string]any{
		"id":       "req-2",
		"provider": "codex",
	})
	if resp["exit_code"] != 1 {
		t.Errorf("expected exit_code=1, got %v", resp["exit_code"])
	}
	reply, _ := resp["reply"].(string)
	if !strings.Contains(reply, "Missing 'caller'") {
		t.Errorf("expected Missing 'caller' reply, got %q", reply)
	}
}

func TestHandleRequest_UnknownProvider(t *testing.T) {
	d := newTestDaemon(t, &stubAdapter{key: "codex"})
	resp := d.handleRequest(map[string]any{
		"id":       "req-3",
		"provider": "mystery",
		"caller":   "claude",
	})
	if resp["exit_code"] != 1 {
		t.Errorf("expected exit_code=1, got %v", resp["exit_code"])
	}
	reply, _ := resp["reply"].(string)
	if !strings.Contains(reply, "Unknown provider") {
		t.Errorf("expected Unknown provider reply, got %q", reply)
	}
}

func TestHandleRequest_SuccessRoundTrip(t *testing.T) {
	stub := &stubAdapter{key: "codex"}
	d := newTestDaemon(t, stub)

	resp := d.handleRequest(map[string]any{
		"id":        "req-4",
		"provider":  "codex",
		"caller":    "claude",
		"message":   "hello world",
		"timeout_s": float64(5),
	})

	if resp["exit_code"] != 0 {
		t.Errorf("expected exit_code=0, got %v (reply=%v)", resp["exit_code"], resp["reply"])
	}
	reply, _ := resp["reply"].(string)
	if reply != "stub-reply-for:hello world" {
		t.Errorf("expected 'stub-reply-for:hello world', got %q", reply)
	}
	if resp["provider"] != "codex" {
		t.Errorf("provider echo mismatch: %v", resp["provider"])
	}
	meta, ok := resp["meta"].(map[string]any)
	if !ok {
		t.Fatalf("expected meta map, got %T", resp["meta"])
	}
	if meta["status"] != "completed" {
		t.Errorf("meta.status=%v, want completed", meta["status"])
	}
	if stub.handleCalled != 1 {
		t.Errorf("stub.HandleTask called %d times, want 1", stub.handleCalled)
	}
}

func TestHandleRequest_QualifiedProviderKey(t *testing.T) {
	// Qualified names like "codex:alt" route to the base ("codex") adapter
	// with Instance="alt" plumbed through.
	var seenInstance string
	stub := &stubAdapter{
		key: "codex",
		handleFunc: func(task *adapter.QueuedTask) *adapter.ProviderResult {
			seenInstance = task.Request.Instance
			return &adapter.ProviderResult{
				ExitCode: 0, Reply: "ok", ReqID: task.ReqID,
				Status: "completed", DoneSeen: true,
			}
		},
	}
	d := newTestDaemon(t, stub)

	resp := d.handleRequest(map[string]any{
		"id":        "req-5",
		"provider":  "codex:alt",
		"caller":    "claude",
		"message":   "m",
		"timeout_s": float64(5),
	})
	if resp["exit_code"] != 0 {
		t.Fatalf("expected success, got exit_code=%v reply=%v", resp["exit_code"], resp["reply"])
	}
	if seenInstance != "alt" {
		t.Errorf("expected Instance='alt', got %q", seenInstance)
	}
	if resp["provider"] != "codex:alt" {
		t.Errorf("qualified name should echo unchanged: %v", resp["provider"])
	}
}

func TestHandleRequest_TimeoutCancelsTask(t *testing.T) {
	// HandleTask that blocks longer than the request's timeout_s;
	// handleRequest should bail with exit_code=2 and empty reply after
	// the timeout grace period (timeout_s + 5s).
	stub := &stubAdapter{
		key: "codex",
		handleFunc: func(task *adapter.QueuedTask) *adapter.ProviderResult {
			// Wait for cancellation or a long time.
			select {
			case <-task.CancelEvent:
			case <-time.After(30 * time.Second):
			}
			// Don't set a result — let the daemon observe nil GetResult().
			return nil
		},
	}
	d := newTestDaemon(t, stub)

	// timeout_s is negative → handleRequest waits up to 24h, so use a
	// tiny positive timeout + we intercept cancel path.
	// Actually the real grace is timeout_s + 5s; to keep the test fast
	// we'd need to set timeout_s to something small, and accept that
	// the test takes ~5s. That's too slow. Instead, directly cancel.
	//
	// Simpler approach: submit, grab the task via pool, cancel it ourselves.
	// But handleRequest doesn't expose the task. So we rely on the
	// grace-plus-timeout. Keep timeout_s small.
	//
	// We trade a ~6s test runtime for end-to-end coverage of the timer
	// branch. Gate behind testing.Short() so `go test -short` skips it.
	if testing.Short() {
		t.Skip("timeout path exercises a 6s timer; skipped under -short")
	}

	start := time.Now()
	resp := d.handleRequest(map[string]any{
		"id":        "req-6",
		"provider":  "codex",
		"caller":    "claude",
		"message":   "m",
		"timeout_s": float64(1), // grace is 1+5 = 6s
	})
	elapsed := time.Since(start)
	if elapsed < 5*time.Second {
		t.Errorf("expected >5s wait, got %v", elapsed)
	}
	if resp["exit_code"] != 2 {
		t.Errorf("expected exit_code=2 on timeout, got %v", resp["exit_code"])
	}
}

// ── constructor / pool behaviour ──

func TestNewUnifiedAskDaemon_StateFileDefault(t *testing.T) {
	reg := registry.New()
	reg.Register(&stubAdapter{key: "codex"})
	d := NewUnifiedAskDaemon("", 0, "", reg, "")
	// Empty stateFile arg should default to runtime.StateFilePath(...).
	// We don't care about the exact path (it depends on $HOME) but it
	// must not remain empty.
	if d.stateFile == "" {
		t.Error("expected stateFile to be set when caller passes empty")
	}
	if !strings.HasSuffix(d.stateFile, AskdSpec.StateFileName) {
		t.Errorf("expected stateFile to end with %q, got %q",
			AskdSpec.StateFileName, d.stateFile)
	}
}

func TestNewUnifiedAskDaemon_CustomStateFilePreserved(t *testing.T) {
	reg := registry.New()
	d := NewUnifiedAskDaemon("h", 1, "/explicit/path.json", reg, "/work")
	if d.stateFile != "/explicit/path.json" {
		t.Errorf("custom stateFile overridden: %q", d.stateFile)
	}
	if d.WorkDir != "/work" {
		t.Errorf("WorkDir mismatch: %q", d.WorkDir)
	}
	if d.Host != "h" || d.Port != 1 {
		t.Errorf("Host/Port not plumbed through: %q:%d", d.Host, d.Port)
	}
}

func TestUnifiedWorkerPool_GetPoolCachesPerProvider(t *testing.T) {
	reg := registry.New()
	reg.Register(&stubAdapter{key: "codex"})
	reg.Register(&stubAdapter{key: "gemini"})
	p := newUnifiedWorkerPool(reg)

	c1 := p.getPool("codex")
	c2 := p.getPool("codex")
	g1 := p.getPool("gemini")
	if c1 != c2 {
		t.Error("getPool(codex) should return the same cached instance")
	}
	if c1 == g1 {
		t.Error("getPool(codex) and getPool(gemini) must be distinct")
	}
}

func TestUnifiedWorkerPool_GetPoolConcurrentSafe(t *testing.T) {
	reg := registry.New()
	reg.Register(&stubAdapter{key: "codex"})
	p := newUnifiedWorkerPool(reg)

	// 20 goroutines all hammer getPool("codex") — the returned pointer
	// must be stable (no second instance created) and no -race complaint.
	var wg sync.WaitGroup
	results := make([]any, 20)
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = p.getPool("codex")
		}(i)
	}
	wg.Wait()

	first := results[0]
	for i, r := range results {
		if r != first {
			t.Fatalf("goroutine %d got a different pool instance", i)
		}
	}
}

// ── trivial constant sanity ──

func TestAskdSpec_ExpectedFields(t *testing.T) {
	if AskdSpec.DaemonKey != "cxb-askd" {
		t.Errorf("DaemonKey=%q", AskdSpec.DaemonKey)
	}
	if AskdSpec.ProtocolPrefix != "cxb-ask" {
		t.Errorf("ProtocolPrefix=%q", AskdSpec.ProtocolPrefix)
	}
	if AskdSpec.StateFileName != "cxb-askd.json" {
		t.Errorf("StateFileName=%q", AskdSpec.StateFileName)
	}
	if AskdSpec.LogFileName != "cxb-askd.log" {
		t.Errorf("LogFileName=%q", AskdSpec.LogFileName)
	}
}

func TestNowMs_Monotonic(t *testing.T) {
	a := nowMs()
	time.Sleep(2 * time.Millisecond)
	b := nowMs()
	if b <= a {
		t.Errorf("nowMs not monotonic: a=%d b=%d", a, b)
	}
}

// ── queuedTaskAdapter unit behaviour ──

func TestQueuedTaskAdapter_SetResultClosesDoneEvent(t *testing.T) {
	inner := &adapter.QueuedTask{
		ReqID:     "id-1",
		DoneEvent: make(chan struct{}),
	}
	wrapper := &queuedTaskAdapter{inner: inner}

	want := &adapter.ProviderResult{ReqID: "id-1", Reply: "ok"}
	wrapper.SetResult(want)

	select {
	case <-inner.DoneEvent:
		// ok
	case <-time.After(time.Second):
		t.Fatal("SetResult should close inner.DoneEvent")
	}
	if inner.GetResult() != want {
		t.Errorf("inner.Result not propagated: %v", inner.GetResult())
	}
	if wrapper.ReqID() != "id-1" {
		t.Errorf("ReqID() mismatch: %q", wrapper.ReqID())
	}
}

func TestQueuedTaskAdapter_IsCancelledProxies(t *testing.T) {
	inner := &adapter.QueuedTask{
		ReqID:       "id-2",
		DoneEvent:   make(chan struct{}),
		CancelEvent: make(chan struct{}),
	}
	wrapper := &queuedTaskAdapter{inner: inner}

	if wrapper.IsCancelled() {
		t.Error("fresh wrapper should not report cancelled")
	}
	inner.Cancel()
	if !wrapper.IsCancelled() {
		t.Error("wrapper should report cancelled after inner.Cancel()")
	}
}

func TestQueuedTaskAdapter_SetResultWithNonProviderResultNoop(t *testing.T) {
	// SetResult with a non-*adapter.ProviderResult value must not panic
	// and must not close the inner DoneEvent (we only propagate typed
	// results to the inner task).
	inner := &adapter.QueuedTask{
		ReqID:     "id-3",
		DoneEvent: make(chan struct{}),
	}
	wrapper := &queuedTaskAdapter{inner: inner}
	wrapper.SetResult("not a provider result")

	select {
	case <-inner.DoneEvent:
		t.Error("DoneEvent should remain open for non-typed SetResult")
	default:
		// expected
	}
	if wrapper.result != "not a provider result" {
		t.Errorf("wrapper.result not stored: %v", wrapper.result)
	}
}

// Smoke-check that newSessionWorker plumbs adapter.Key through. Ensures
// the wiring survives any future refactor of the handler/error bridge.
func TestNewSessionWorker_PlumbsAdapter(t *testing.T) {
	stub := &stubAdapter{key: "codex"}
	w := newSessionWorker("codex:test", stub)
	if w == nil {
		t.Fatal("newSessionWorker returned nil")
	}
	// We can't introspect the worker's closures, but we can ensure
	// construction doesn't panic for the full supported adapter shape.
	_ = fmt.Sprintf("%T", w)
}
