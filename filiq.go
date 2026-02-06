// Package filiq provides a lightweight, dependency-free, high-performance in-memory worker pool.
// It supports both FIFO (Queue) and LIFO (Stack) processing modes with optional bounded buffering,
// utilizing sync.Cond for efficient signaling and optimization strategies like head-index tracking
// and lazy compaction to minimize memory allocations.
package filiq

import (
	"context"
	"sync"
)

// Mode represents the processing order of tasks.
type Mode int

const (
	// FIFO (First-In-First-Out) processes tasks in the order they were added (Queue).
	FIFO Mode = iota
	// LIFO (Last-In-First-Out) processes the most recently added tasks first (Stack).
	LIFO
)

// Runner manages a pool of workers to execute tasks.
type Runner struct {
	mu           sync.Mutex
	cond         *sync.Cond
	tasks        []func()
	head         int // Head index for FIFO popping to avoid re-slicing
	mode         Mode
	workers      int
	maxBuffer    int            // 0 means unbounded
	wg           sync.WaitGroup // Waits for workers to finish
	stopped      bool
	panicHandler func(interface{})
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

// WithPanicHandler sets a callback to be invoked when a task panics.
// The worker will recover and continue processing subsequent tasks.
func WithPanicHandler(handler func(interface{})) Option {
	return func(r *Runner) {
		r.panicHandler = handler
	}
}

// New creates a new Runner with the given options and starts the workers.
func New(opts ...Option) *Runner {
	r := &Runner{
		mode:      FIFO,
		workers:   1,
		maxBuffer: 0, // Unbounded by default
	}

	for _, opt := range opts {
		opt(r)
	}

	r.cond = sync.NewCond(&r.mu)

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
		for (len(r.tasks) - r.head) == 0 {
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

		if r.mode == FIFO {
			// FIFO
			task = r.tasks[r.head]
			r.tasks[r.head] = nil // Avoid memory leak
			r.head++
		} else {
			// LIFO
			lastIdx := len(r.tasks) - 1
			task = r.tasks[lastIdx]
			r.tasks[lastIdx] = nil // Avoid memory leak
			r.tasks = r.tasks[:lastIdx]
		}

		// Cleanup if empty to recover capacity instantly
		if r.head == len(r.tasks) {
			r.tasks = r.tasks[:0]
			r.head = 0
		}

		// Signal to Put/TryPut that space might be available (if bounded)
		if r.maxBuffer > 0 {
			r.cond.Signal() // We consumed one, so wake up a producer if they are waiting
		}

		r.mu.Unlock()

		// Execute task outside lock
		if task != nil {
			func() {
				defer func() {
					if p := recover(); p != nil {
						if r.panicHandler != nil {
							r.panicHandler(p)
						}
					}
				}()
				task()
			}()
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
		for (len(r.tasks) - r.head) >= r.maxBuffer {
			// Check stopped again while waiting
			if r.stopped {
				return false
			}
			r.cond.Wait()
		}
		if r.stopped {
			return false
		}
	}

	r.compactIfNeeded()
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

	if r.maxBuffer > 0 && (len(r.tasks)-r.head) >= r.maxBuffer {
		return false
	}

	r.compactIfNeeded()
	r.tasks = append(r.tasks, task)
	r.cond.Signal()
	return true
}

// Shutdown signals all workers to stop and waits for them to finish current tasks.
// It respects the provided context for timeout or cancellation.
// Tasks remaining in the queue will be discarded.
func (r *Runner) Shutdown(ctx context.Context) error {
	r.mu.Lock()
	if !r.stopped {
		r.stopped = true

		r.tasks = nil // Discard queued tasks
		r.head = 0    // Reset head

		r.cond.Broadcast() // Wake up ALL workers and producers so they check 'stopped'
	}
	r.mu.Unlock()

	// Wait for workers to return, or context to be done
	c := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(c)
	}()
	select {
	case <-c:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// compactIfNeeded shifts elements to the front if the slice is full at the end
// but has free space at the beginning.
func (r *Runner) compactIfNeeded() {
	if len(r.tasks) == cap(r.tasks) && r.head > 0 {
		// Slice bounds are full, but we have space at the front.
		// Compact the slice to reclaim space.
		copy(r.tasks, r.tasks[r.head:])
		r.tasks = r.tasks[:len(r.tasks)-r.head]
		r.head = 0
	}
}
