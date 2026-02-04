package filiq

import (
	"context"
	"sync"
)

// Mode represents the processing order of tasks.
type Mode int

const (
	FIFO Mode = iota // First-In-First-Out (Queue)
	LIFO             // Last-In-First-Out (Stack)
)

// Runner manages a pool of workers to execute tasks.
type Runner struct {
	mu         sync.Mutex
	cond       *sync.Cond
	tasks      []func()
	mode       Mode
	workers    int
	maxBuffer  int // 0 means unbounded
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup // Waits for workers to finish
	stopped    bool
	workerIdle int // Counter for idle workers (for optimization/monitoring if needed)
}

// Option configures a Runner.
type Option func(*Runner)

// WithFIFO sets the processing mode to FIFO (Queue).
func WithFIFO() Option {
	return func(r *Runner) {
		r.mode = FIFO
	}
}

// WithLIFO sets the processing mode to LIFO (Stack).
func WithLIFO() Option {
	return func(r *Runner) {
		r.mode = LIFO
	}
}

// WithWorkers sets the number of concurrent workers. Default is 1.
func WithWorkers(n int) Option {
	return func(r *Runner) {
		if n > 0 {
			r.workers = n
		}
	}
}

// WithBufferSize sets the maximum number of tasks in the buffer.
// 0 means unbounded (default).
func WithBufferSize(size int) Option {
	return func(r *Runner) {
		if size >= 0 {
			r.maxBuffer = size
		}
	}
}

// WithContext sets the parent context for the Runner.
func WithContext(ctx context.Context) Option {
	return func(r *Runner) {
		r.ctx = ctx
	}
}

// New creates a new Runner with the given options and starts the workers.
func New(opts ...Option) *Runner {
	r := &Runner{
		mode:      FIFO,
		workers:   1,
		maxBuffer: 0, // Unbounded by default
		ctx:       context.Background(),
	}

	for _, opt := range opts {
		opt(r)
	}

	// Create a cancelable context for internal worker control, inheriting from user context
	r.ctx, r.cancel = context.WithCancel(r.ctx)
	r.cond = sync.NewCond(&r.mu)

	// Monitor for cancellation to trigger graceful shutdown
	go func() {
		<-r.ctx.Done()
		r.Stop()
	}()

	r.startWorkers()
	return r
}

func (r *Runner) startWorkers() {
	r.wg.Add(r.workers)
	for i := 0; i < r.workers; i++ {
		go r.workerLoop()
	}
}

func (r *Runner) workerLoop() {
	defer r.wg.Done()

	for {
		r.mu.Lock()

		// Wait while there are no tasks AND the runner hasn't been stopped.
		// If stopped is true, we still process remaining tasks until empty.
		for len(r.tasks) == 0 {
			if r.stopped {
				// No more tasks and stopped -> exit
				r.mu.Unlock()
				return
			}
			// Wait for a signal (new task or stop)
			r.cond.Wait()
		}

		// Pop task based on mode
		var task func()
		lastIdx := len(r.tasks) - 1

		if r.mode == LIFO {
			// LIFO: Take from the end
			task = r.tasks[lastIdx]
			r.tasks[lastIdx] = nil // Avoid memory leak
			r.tasks = r.tasks[:lastIdx]
		} else {
			// FIFO: Take from the front
			// Optimization: For a slice-based queue, taking from [0] involves shifting or slicing.
			// r.tasks = r.tasks[1:] works but underlying array grows.
			// A ring buffer would be better for massive queues, but for simplicity/robustness:
			task = r.tasks[0]
			r.tasks[0] = nil // Avoid memory leak
			r.tasks = r.tasks[1:]

			// Optional: compaction could happen here if capability > len*2 etc.
		}

		// Signal to Put/TryPut that space might be available (if bounded)
		if r.maxBuffer > 0 {
			r.cond.Signal() // We consumed one, so wake up a producer if they are waiting
		}

		r.mu.Unlock()

		// Execute task outside lock
		if task != nil {
			task()
		}
	}
}

// Put adds a task to the pool.
// If the buffer is full (bounded mode), it blocks until space is available.
// Returns false if the runner is stopped.
func (r *Runner) Put(task func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return false
	}

	// If bounded, wait until there is space
	if r.maxBuffer > 0 {
		for len(r.tasks) >= r.maxBuffer {
			// Check stopped again while waiting
			if r.stopped {
				return false
			}
			r.cond.Wait()
		}
	}

	r.tasks = append(r.tasks, task)
	r.cond.Signal() // Wake up one worker
	return true
}

// TryPut attempts to add a task to the pool without blocking.
// Returns false if the buffer is full or the runner is stopped.
func (r *Runner) TryPut(task func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return false
	}

	if r.maxBuffer > 0 && len(r.tasks) >= r.maxBuffer {
		return false
	}

	r.tasks = append(r.tasks, task)
	r.cond.Signal()
	return true
}

// Stop signals all workers to stop and waits for them to finish current tasks.
// Tasks remaining in the queue will be discarded.
func (r *Runner) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true

	r.tasks = nil // Discard queued tasks

	r.cond.Broadcast() // Wake up ALL workers so they check 'stopped'

	// Also wake up any blocked producers
	if r.maxBuffer > 0 {
		// If producers are waiting on full buffer, they need to wake up and see stopped=true
		// Broadcast handles them too if they share the cond?
		// Yes, we actully share the cond for both workers and producers in this simple design.
		// Wait, usually we use distinct condition variables or broadcast for everyone.
		// Broadcast() wakes everyone.
	}

	r.mu.Unlock()

	r.cancel()  // Cancel context
	r.wg.Wait() // Wait for workers to return
}
