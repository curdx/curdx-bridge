package registry

import (
	"sort"
	"sync"
	"testing"

	"github.com/curdx/curdx-bridge/internal/adapter"
	"github.com/curdx/curdx-bridge/internal/providers"
)

// stubAdapter is a minimal adapter.BaseProviderAdapter implementation for
// registry tests. Callbacks are counted so StartAll/StopAll behaviour can
// be asserted without wiring up real provider logic.
type stubAdapter struct {
	key        string
	startCount int
	stopCount  int
	panicOn    string // "start" or "stop" to force a panic
	mu         sync.Mutex
}

func (s *stubAdapter) Key() string                                   { return s.key }
func (s *stubAdapter) Spec() providers.ProviderDaemonSpec            { return providers.ProviderDaemonSpec{} }
func (s *stubAdapter) SessionFilename() string                       { return "." + s.key + "-session" }
func (s *stubAdapter) LoadSession(string, string) (any, error)       { return nil, nil }
func (s *stubAdapter) ComputeSessionKey(any, string) string          { return s.key + ":x" }
func (s *stubAdapter) HandleTask(*adapter.QueuedTask) *adapter.ProviderResult {
	return &adapter.ProviderResult{}
}
func (s *stubAdapter) HandleException(err error, task *adapter.QueuedTask) *adapter.ProviderResult {
	return adapter.DefaultHandleException(s.key, err, task)
}

func (s *stubAdapter) OnStart() {
	s.mu.Lock()
	s.startCount++
	s.mu.Unlock()
	if s.panicOn == "start" {
		panic("intentional start panic")
	}
}

func (s *stubAdapter) OnStop() {
	s.mu.Lock()
	s.stopCount++
	s.mu.Unlock()
	if s.panicOn == "stop" {
		panic("intentional stop panic")
	}
}

func (s *stubAdapter) starts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.startCount
}

func (s *stubAdapter) stops() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopCount
}

// ── core API ──

func TestNew_EmptyRegistry(t *testing.T) {
	r := New()
	if r == nil {
		t.Fatal("New() returned nil")
	}
	if keys := r.Keys(); len(keys) != 0 {
		t.Errorf("fresh registry should have no keys, got %v", keys)
	}
	if all := r.All(); len(all) != 0 {
		t.Errorf("fresh registry should have no adapters, got %v", all)
	}
	if got := r.Get("anything"); got != nil {
		t.Errorf("Get on empty registry should return nil, got %v", got)
	}
}

func TestRegister_StoresByKey(t *testing.T) {
	r := New()
	a := &stubAdapter{key: "codex"}
	r.Register(a)

	got := r.Get("codex")
	if got != a {
		t.Errorf("Get returned wrong instance: %v != %v", got, a)
	}
	if keys := r.Keys(); len(keys) != 1 || keys[0] != "codex" {
		t.Errorf("Keys() mismatch: %v", keys)
	}
	if all := r.All(); len(all) != 1 {
		t.Errorf("All() should have 1 entry, got %d", len(all))
	}
}

func TestRegister_DuplicateKeyReplaces(t *testing.T) {
	r := New()
	first := &stubAdapter{key: "codex"}
	second := &stubAdapter{key: "codex"}
	r.Register(first)
	r.Register(second)

	// Last-write-wins — matches Python behaviour.
	if got := r.Get("codex"); got != second {
		t.Errorf("expected second registration to win")
	}
	if keys := r.Keys(); len(keys) != 1 {
		t.Errorf("duplicate key should not create two entries, got %v", keys)
	}
}

func TestKeys_ReturnsAllRegistered(t *testing.T) {
	r := New()
	r.Register(&stubAdapter{key: "codex"})
	r.Register(&stubAdapter{key: "gemini"})
	r.Register(&stubAdapter{key: "opencode"})
	r.Register(&stubAdapter{key: "claude"})

	keys := r.Keys()
	sort.Strings(keys)
	want := []string{"claude", "codex", "gemini", "opencode"}
	if len(keys) != len(want) {
		t.Fatalf("want %d keys, got %v", len(want), keys)
	}
	for i, k := range want {
		if keys[i] != k {
			t.Errorf("Keys[%d]=%q, want %q", i, keys[i], k)
		}
	}
}

func TestAll_ReturnsAllAdapters(t *testing.T) {
	r := New()
	a := &stubAdapter{key: "codex"}
	b := &stubAdapter{key: "gemini"}
	r.Register(a)
	r.Register(b)

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("want 2 adapters, got %d", len(all))
	}
	found := map[string]bool{}
	for _, x := range all {
		found[x.Key()] = true
	}
	if !found["codex"] || !found["gemini"] {
		t.Errorf("All() missed entries: %v", found)
	}
}

// ── Start/Stop callbacks ──

func TestStartAll_FiresOnStartForEach(t *testing.T) {
	r := New()
	a := &stubAdapter{key: "codex"}
	b := &stubAdapter{key: "gemini"}
	r.Register(a)
	r.Register(b)

	r.StartAll()
	if a.starts() != 1 {
		t.Errorf("codex OnStart count=%d, want 1", a.starts())
	}
	if b.starts() != 1 {
		t.Errorf("gemini OnStart count=%d, want 1", b.starts())
	}
}

func TestStopAll_FiresOnStopForEach(t *testing.T) {
	r := New()
	a := &stubAdapter{key: "codex"}
	b := &stubAdapter{key: "gemini"}
	r.Register(a)
	r.Register(b)

	r.StopAll()
	if a.stops() != 1 {
		t.Errorf("codex OnStop count=%d, want 1", a.stops())
	}
	if b.stops() != 1 {
		t.Errorf("gemini OnStop count=%d, want 1", b.stops())
	}
}

func TestStartAll_RecoversFromPanic(t *testing.T) {
	// One adapter panicking in OnStart must not break subsequent adapters.
	r := New()
	bad := &stubAdapter{key: "bad", panicOn: "start"}
	good := &stubAdapter{key: "good"}
	r.Register(bad)
	r.Register(good)

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("StartAll should recover internally, leaked panic: %v", rec)
		}
	}()
	r.StartAll()

	// Even though bad panicked, good should still have been started.
	if good.starts() != 1 {
		t.Errorf("good.OnStart should have been called despite bad's panic")
	}
}

func TestStopAll_RecoversFromPanic(t *testing.T) {
	r := New()
	bad := &stubAdapter{key: "bad", panicOn: "stop"}
	good := &stubAdapter{key: "good"}
	r.Register(bad)
	r.Register(good)

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("StopAll should recover internally, leaked panic: %v", rec)
		}
	}()
	r.StopAll()

	if good.stops() != 1 {
		t.Errorf("good.OnStop should have been called despite bad's panic")
	}
}

// ── Concurrency ──

func TestConcurrentRegisterAndGet(t *testing.T) {
	r := New()

	var wg sync.WaitGroup
	// 10 writers register different keys.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r.Register(&stubAdapter{key: makeKey(idx)})
		}(i)
	}
	// 10 readers hammer Get + Keys simultaneously.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Keys()
			_ = r.Get("codex")
			_ = r.All()
		}()
	}
	wg.Wait()

	if keys := r.Keys(); len(keys) != 10 {
		t.Errorf("expected 10 keys after concurrent writes, got %d", len(keys))
	}
}

func makeKey(i int) string {
	// Stable key names for the concurrent test so each goroutine lands on
	// a unique map entry.
	return "prov-" + string(rune('a'+i))
}
