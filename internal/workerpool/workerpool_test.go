package workerpool

import (
	"sync"
	"testing"
)

// --- test helpers ---

type testTask struct {
	reqID     string
	cancelled bool
	result    interface{}
	done      chan struct{}
}

func newTestTask(reqID string) *testTask {
	return &testTask{
		reqID: reqID,
		done:  make(chan struct{}, 1),
	}
}

func (t *testTask) ReqID() string        { return t.reqID }
func (t *testTask) IsCancelled() bool     { return t.cancelled }
func (t *testTask) SetResult(v interface{}) { t.result = v }
func (t *testTask) Signal()               { select { case t.done <- struct{}{}: default: } }
func (t *testTask) Wait()                 { <-t.done }

// --- tests ---

func TestPerSessionWorkerPoolReusesSameKey(t *testing.T) {
	var mu sync.Mutex
	started := map[string]int{}

	factory := func(key string) *BaseSessionWorker {
		mu.Lock()
		started[key]++
		mu.Unlock()
		return NewBaseSessionWorker(key,
			func(task Task) (interface{}, error) { return nil, nil },
			func(err error, task Task) interface{} { return nil },
		)
	}

	pool := NewPerSessionWorkerPool()
	w1 := pool.GetOrCreate("k1", factory)
	w2 := pool.GetOrCreate("k1", factory)
	w3 := pool.GetOrCreate("k2", factory)

	if w1 != w2 {
		t.Error("expected same worker for same key")
	}
	if w1 == w3 {
		t.Error("expected different worker for different key")
	}

	mu.Lock()
	if started["k1"] != 1 {
		t.Errorf("k1 started %d times, want 1", started["k1"])
	}
	if started["k2"] != 1 {
		t.Errorf("k2 started %d times, want 1", started["k2"])
	}
	mu.Unlock()

	w1.Stop()
	w3.Stop()
}

func TestBaseSessionWorkerProcessesTask(t *testing.T) {
	w := NewBaseSessionWorker("s1",
		func(task Task) (interface{}, error) {
			return "ok:" + task.ReqID(), nil
		},
		func(err error, task Task) interface{} {
			return "err:" + task.ReqID() + ":" + err.Error()
		},
	)
	w.Start()
	defer w.Stop()

	task := newTestTask("r1")
	w.Enqueue(task)
	task.Wait()

	got, ok := task.result.(string)
	if !ok || got != "ok:r1" {
		t.Errorf("result = %v, want ok:r1", task.result)
	}
}

func TestBaseSessionWorkerExceptionPath(t *testing.T) {
	w := NewBaseSessionWorker("s1",
		func(task Task) (interface{}, error) {
			return nil, &testError{"boom"}
		},
		func(err error, task Task) interface{} {
			return "err:" + task.ReqID() + ":" + err.Error()
		},
	)
	w.Start()
	defer w.Stop()

	task := newTestTask("r2")
	w.Enqueue(task)
	task.Wait()

	got, ok := task.result.(string)
	if !ok {
		t.Fatalf("result = %v, want string", task.result)
	}
	if got != "err:r2:boom" {
		t.Errorf("result = %q, want err:r2:boom", got)
	}
}

func TestBaseSessionWorkerSkipsCancelledTasks(t *testing.T) {
	w := NewBaseSessionWorker("s1",
		func(task Task) (interface{}, error) {
			return "processed", nil
		},
		func(err error, task Task) interface{} { return nil },
	)
	w.Start()
	defer w.Stop()

	task := newTestTask("r3")
	task.cancelled = true
	w.Enqueue(task)
	task.Wait()

	if task.result != nil {
		t.Errorf("cancelled task should have nil result, got %v", task.result)
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
