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
	defer r.Stop()

	var result []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	count := 10
	wg.Add(count)

	for i := 0; i < count; i++ {
		val := i
		r.Put(func() {
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
	defer r.Stop()

	var result []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	count := 10
	// For LIFO test, we need to make sure tasks are queued BEFORE the worker picks them up.
	// Otherwise, if the worker is too fast, it acts like FIFO (processes as soon as it arrives).
	// But `r.Put` signals immediately.
	// To test LIFO, we can pause the worker?
	// Or we can fill the queue before starting?
	// The current API starts workers immediately in `New`.

	// Workaround: Flood the queue faster than the worker can consume.
	// Or use a "blocker" task.

	wg.Add(count)

	// Add a blocker task first
	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)
	r.Put(func() {
		blockerWg.Wait() // Block the single worker
	})

	// Now queue up items 0..9
	for i := 0; i < count; i++ {
		val := i
		r.Put(func() {
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
	defer r.Stop()

	var wg sync.WaitGroup
	count := 100
	wg.Add(count)

	var counter int64

	for i := 0; i < count; i++ {
		r.Put(func() {
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

func TestBoundedBufferBlocks(t *testing.T) {
	// Buffer size 1, Worker count 0 (simulated by having 1 worker blocked)
	// Actually we can't set 0 workers, so we use 1 worker and block it.
	r := filiq.New(filiq.WithBufferSize(1), filiq.WithWorkers(1))

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// Task 1: Consumed immediately by worker, but blocks inside
	r.Put(func() {
		blockerWg.Wait()
	})

	// Wait a bit for worker to pick it up
	time.Sleep(50 * time.Millisecond)

	// Now buffer is empty (worker took it).
	// Task 2: Goes into buffer [1/1]
	done2 := make(chan struct{})
	go func() {
		r.Put(func() {})
		close(done2)
	}()
	<-done2 // Should return quickly

	// Task 3: Should block because buffer is full (Test 2 is in buffer)
	done3 := make(chan struct{})
	go func() {
		r.Put(func() {})
		close(done3)
	}()

	select {
	case <-done3:
		t.Fatal("Put should have blocked on full buffer")
	case <-time.After(100 * time.Millisecond):
		// Expected behavior: timeout because it's blocked
	}

	// TryPut should return false immediately
	if r.TryPut(func() {}) {
		t.Error("TryPut should return false when buffer is full")
	}

	// Unblock everything
	blockerWg.Done()
	<-done3 // Should finish now
	r.Stop()
}

func TestGracefulShutdownDiscards(t *testing.T) {
	r := filiq.New(filiq.WithWorkers(1))

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// Task 1: Block the worker
	started := make(chan struct{})
	r.Put(func() {
		close(started)
		blockerWg.Wait()
	})

	<-started

	// Task 2: In queue
	var task2Ran int64
	r.Put(func() {
		atomic.StoreInt64(&task2Ran, 1)
	})

	// Stop() should signal exit.
	// But first we need to unblock worker so it can finish Task 1.
	// Wait, Stop() waits for workers. So if we hold the lock in Stop(), we can't unblock?
	// No, Stop() calls cancel(), but here the worker is blocked on `blockerWg.Wait()`.
	// We need to unblock the worker from the outside *after* Stop is called?
	// Or unblock it before?

	// If we call Stop(), it waits for wg.
	// If the worker is stuck on blockerWg which is controlled by us, main goroutine is blocked on Stop().
	// So we need to unblock in a separate goroutine.

	go func() {
		time.Sleep(100 * time.Millisecond) // Ensure Stop() is entered
		blockerWg.Done()
	}()

	// This will block until Task 1 finishes.
	// Task 2 should be discarded based on README ("Any tasks remaining... are discarded")
	r.Stop()

	if atomic.LoadInt64(&task2Ran) == 1 {
		t.Error("Task 2 should have been discarded")
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := filiq.New(filiq.WithWorkers(1), filiq.WithContext(ctx))

	// Ensure runner is working
	executed := make(chan struct{})
	r.Put(func() {
		close(executed)
	})
	<-executed

	// Cancel context
	cancel()

	// Wait for shutdown (Stop waits for workers, so if Stop is called, this should verify via side effect)
	// How to verify Stop was called?
	// We can try to Put something and expect it to fail if it's eventually stopped.
	// But Put blocks if we don't have buffer space and workers are stopped?
	// No, Put checks `if r.stopped { return false }`.

	// Retry loop to wait for async stop propagation
	timeout := time.After(1 * time.Second)
	stopped := false
	for {
		select {
		case <-timeout:
			t.Fatal("Runner did not stop after context cancellation")
		default:
			if !r.Put(func() {}) {
				stopped = true
				break
			}
			time.Sleep(10 * time.Millisecond) // Give time for monitoring goroutine to react
		}
		if stopped {
			break
		}
	}
}
