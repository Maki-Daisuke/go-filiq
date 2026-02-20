// Package filiq_test contains integration tests and benchmarks for the filiq package.
package filiq_test

import (
	"context"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Maki-Daisuke/go-filiq"
)

func TestFIFO(t *testing.T) {
	// Single worker to guarantee order check
	r := filiq.New(filiq.WithWorkers(1), filiq.WithFIFO())
	defer r.Shutdown(context.Background())

	var result []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	count := 10
	wg.Add(count)

	for i := 0; i < count; i++ {
		val := i
		r.Submit(func() {
			defer wg.Done()
			// Simulate small work
			time.Sleep(10 * time.Millisecond)
			mu.Lock()
			result = append(result, val)
			mu.Unlock()
		})
	}

	wg.Wait()

	if len(result) != count {
		t.Fatalf("expected length %d, got %d", count, len(result))
	}
	for i := 0; i < count; i++ {
		if result[i] != i {
			t.Errorf("expected index %d to be %d, got %d", i, i, result[i])
		}
	}
}

func TestLIFO(t *testing.T) {
	// Single worker to guarantee order check
	r := filiq.New(filiq.WithWorkers(1), filiq.WithLIFO())
	defer r.Shutdown(context.Background())

	var result []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	count := 10
	// For LIFO test, we need to make sure tasks are queued BEFORE the worker picks them up.
	// Otherwise, if the worker is too fast, it acts like FIFO (processes as soon as it arrives).
	// But `r.Submit` signals immediately.
	// To test LIFO, we can pause the worker?
	// Or we can fill the queue before starting?
	// The current API starts workers immediately in `New`.

	// Workaround: Flood the queue faster than the worker can consume.
	// Or use a "blocker" task.

	wg.Add(count)

	// Add a blocker task first
	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)
	r.Submit(func() {
		blockerWg.Wait() // Block the single worker
	})

	// Now queue up items 0..9
	for i := 0; i < count; i++ {
		val := i
		r.Submit(func() {
			defer wg.Done()
			mu.Lock()
			result = append(result, val)
			mu.Unlock()
		})
	}

	// Release the worker
	blockerWg.Done()

	wg.Wait()

	// In LIFO, the last added (9) should be first executed.
	// The first added (0) should be last.
	// So we expect: 9, 8, ..., 0
	for i := 0; i < count; i++ {
		expected := count - 1 - i
		if result[i] != expected {
			t.Errorf("LIFO mismatch at index %d: expected %d, got %d", i, expected, result[i])
		}
	}
}

func TestConcurrency(t *testing.T) {
	// Use multiple workers
	r := filiq.New(filiq.WithWorkers(4))
	defer r.Shutdown(context.Background())

	var wg sync.WaitGroup
	count := 100
	wg.Add(count)

	var counter int64

	for i := 0; i < count; i++ {
		r.Submit(func() {
			defer wg.Done()
			atomic.AddInt64(&counter, 1)
			time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)
		})
	}

	wg.Wait()

	if val := atomic.LoadInt64(&counter); val != int64(count) {
		t.Errorf("expected counter %d, got %d", count, val)
	}
}

func TestBoundedBufferReturnsError(t *testing.T) {
	// Buffer size 1, Worker count 0 (simulated by having 1 worker blocked)
	// Actually we can't set 0 workers, so we use 1 worker and block it.
	r := filiq.New(filiq.WithBufferSize(1), filiq.WithWorkers(1))
	defer r.Shutdown(context.Background())

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// User synchronization channel to know when worker has started
	started := make(chan struct{})

	// Task 1: Consumed immediately by worker, but blocks inside
	r.Submit(func() {
		close(started)
		blockerWg.Wait()
	})

	// Wait for worker to pick it up - deterministic
	<-started

	// Now buffer is empty (worker took it).
	// Task 2: Goes into buffer [1/1]
	if err := r.Submit(func() {}); err != nil {
		t.Errorf("Expected nil error for Task 2, got %v", err)
	}

	// Task 3: Should return ErrQueueFull because buffer is full (Test 2 is in buffer)
	err := r.Submit(func() {})
	if err != filiq.ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}

	// Unblock everything
	blockerWg.Done()
}

func TestGracefulShutdownDiscards(t *testing.T) {
	r := filiq.New(filiq.WithWorkers(1))

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// Signal to ensure task 1 is running
	started := make(chan struct{})

	// Task 1: Block the worker
	r.Submit(func() {
		close(started)
		blockerWg.Wait()
	})

	<-started

	// Task 2: In queue
	var task2Ran int64
	r.Submit(func() {
		atomic.StoreInt64(&task2Ran, 1)
	})

	// Stop() should signal exit.
	// We run Shutdown() in a separate goroutine because it will block waiting for Task 1.
	shutdownErr := make(chan error)
	go func() {
		shutdownErr <- r.Shutdown(context.Background())
	}()

	// Wait until we see the runner is stopped.
	// Since Shutdown() holds the lock while setting the stopped flag and clearing the queue,
	// if Submit returns ErrQueueClosed, we know the queue is cleared.
	timeout := time.After(time.Second)
	isStopped := false
	for !isStopped {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for Shutdown to take effect")
		default:
			if r.Submit(func() {}) == filiq.ErrQueueClosed {
				isStopped = true
			} else {
				// Still running, allow context switch
				time.Sleep(time.Millisecond)
			}
		}
	}

	// Unblock the worker. It should finish Task 1 and then see the stopped flag.
	blockerWg.Done()

	// Wait for Shutdown() to return
	if err := <-shutdownErr; err != nil {
		t.Errorf("Shutdown returned error: %v", err)
	}

	if atomic.LoadInt64(&task2Ran) == 1 {
		t.Error("Task 2 should have been discarded")
	}
}

func TestShutdownTimeout(t *testing.T) {
	r := filiq.New(filiq.WithWorkers(1))

	// Start a long running task
	started := make(chan struct{})
	r.Submit(func() {
		close(started)
		time.Sleep(500 * time.Millisecond)
	})

	<-started

	// Try to shutdown with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.Shutdown(ctx)
	duration := time.Since(start)

	if err != context.DeadlineExceeded {
		t.Errorf("expected DeadlineExceeded, got %v", err)
	}

	if duration >= 400*time.Millisecond {
		t.Errorf("Shutdown took too long: %v", duration)
	}
}
