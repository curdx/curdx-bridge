// Package workerpool provides per-session worker goroutines with task queues.
// Source: claude_code_bridge/lib/worker_pool.py
package workerpool

import (
	"context"
	"sync"
)

// Task is the interface that queued tasks must satisfy.
type Task interface {
	// ReqID returns the request identifier for this task.
	ReqID() string
	// IsCancelled returns true if the task was cancelled before processing.
	IsCancelled() bool
	// SetResult assigns the result value; called by the worker after handling.
	SetResult(value any)
	// Signal marks the task as done (unblocks waiters).
	Signal()
}

// TaskHandler processes a single task, returning the result or an error.
type TaskHandler func(task Task) (any, error)

// ErrorHandler converts an error into a fallback result value.
type ErrorHandler func(err error, task Task) any

// BaseSessionWorker is a goroutine-based worker that drains a task channel
// for a specific session key.  It mirrors Python's BaseSessionWorker (daemon thread + queue.Queue).
type BaseSessionWorker struct {
	SessionKey  string
	taskCh      chan Task
	cancel      context.CancelFunc
	ctx         context.Context
	handleTask  TaskHandler
	handleError ErrorHandler
	done        chan struct{} // closed when the run loop exits
	startOnce   sync.Once
	alive       int32 // 1 while running
	mu          sync.Mutex
}

// NewBaseSessionWorker creates a worker bound to sessionKey.
// handleTask and handleError provide the processing logic (equivalent to
// the abstract _handle_task / _handle_exception methods in Python).
func NewBaseSessionWorker(sessionKey string, handleTask TaskHandler, handleError ErrorHandler) *BaseSessionWorker {
	ctx, cancel := context.WithCancel(context.Background())
	return &BaseSessionWorker{
		SessionKey:  sessionKey,
		taskCh:      make(chan Task, 256),
		cancel:      cancel,
		ctx:         ctx,
		handleTask:  handleTask,
		handleError: handleError,
		done:        make(chan struct{}),
	}
}

// Enqueue adds a task to the worker's queue.
func (w *BaseSessionWorker) Enqueue(task Task) {
	select {
	case w.taskCh <- task:
	case <-w.ctx.Done():
		// Worker is stopping; signal the task immediately so callers don't block.
		task.Signal()
	}
}

// Start launches the background goroutine. Safe to call multiple times.
func (w *BaseSessionWorker) Start() {
	w.startOnce.Do(func() {
		w.mu.Lock()
		w.alive = 1
		w.mu.Unlock()
		go w.run()
	})
}

// Stop requests the worker to finish.  Blocks until the run loop exits.
func (w *BaseSessionWorker) Stop() {
	w.cancel()
	<-w.done
}

// IsAlive returns true while the worker goroutine is running.
func (w *BaseSessionWorker) IsAlive() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.alive == 1
}

func (w *BaseSessionWorker) run() {
	defer func() {
		w.mu.Lock()
		w.alive = 0
		w.mu.Unlock()
		close(w.done)
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case task := <-w.taskCh:
			if task == nil {
				continue
			}
			// Skip cancelled/expired tasks.
			if task.IsCancelled() {
				task.Signal()
				continue
			}
			func() {
				defer task.Signal()
				result, err := w.handleTask(task)
				if err != nil {
					result = w.handleError(err, task)
				}
				task.SetResult(result)
			}()
		}
	}
}

// PerSessionWorkerPool manages one worker per session key, replacing dead
// workers automatically.  Mirrors Python's PerSessionWorkerPool.
type PerSessionWorkerPool struct {
	mu      sync.Mutex
	workers map[string]*BaseSessionWorker
}

// NewPerSessionWorkerPool creates an empty pool.
func NewPerSessionWorkerPool() *PerSessionWorkerPool {
	return &PerSessionWorkerPool{
		workers: make(map[string]*BaseSessionWorker),
	}
}

// WorkerFactory creates a new worker for the given session key.
type WorkerFactory func(sessionKey string) *BaseSessionWorker

// GetOrCreate returns the existing worker for sessionKey, or creates one via factory.
// Dead workers are automatically replaced.
func (p *PerSessionWorkerPool) GetOrCreate(sessionKey string, factory WorkerFactory) *BaseSessionWorker {
	p.mu.Lock()
	w, ok := p.workers[sessionKey]
	if ok && !w.IsAlive() {
		// Worker goroutine died; remove and recreate.
		delete(p.workers, sessionKey)
		w = nil
	}
	created := false
	if w == nil {
		w = factory(sessionKey)
		p.workers[sessionKey] = w
		created = true
	}
	p.mu.Unlock()

	if created {
		w.Start()
	}
	return w
}
