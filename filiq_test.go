// Package filiq_test contains integration tests and benchmarks for the filiq package.
package filiq_test

import (
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
	defer r.Stop()

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// User synchronization channel to know when worker has started
	started := make(chan struct{})

	// Task 1: Consumed immediately by worker, but blocks inside
	r.Put(func() {
		close(started)
		blockerWg.Wait()
	})

	// Wait for worker to pick it up - deterministic
	<-started

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
	case <-time.After(50 * time.Millisecond):
		// Expected behavior: timeout because it's blocked
	}

	// TryPut should return false immediately
	if r.TryPut(func() {}) {
		t.Error("TryPut should return false when buffer is full")
	}

	// Unblock everything
	blockerWg.Done()
	<-done3 // Should finish now
}

func TestGracefulShutdownDiscards(t *testing.T) {
	r := filiq.New(filiq.WithWorkers(1))

	blockerWg := sync.WaitGroup{}
	blockerWg.Add(1)

	// Signal to ensure task 1 is running
	started := make(chan struct{})

	// Task 1: Block the worker
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
	// We run Stop() in a separate goroutine because it will block waiting for Task 1.
	stopDone := make(chan struct{})
	go func() {
		r.Stop()
		close(stopDone)
	}()

	// Wait until we see the runner is stopped.
	// Since Stop() holds the lock while setting the stopped flag and clearing the queue,
	// if TryPut returns false (due to stop), we know the queue is cleared.
	timeout := time.After(time.Second)
	isStopped := false
	for !isStopped {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for Stop to take effect")
		default:
			// TryPut returns false if stopped is true
			if !r.TryPut(func() {}) {
				isStopped = true
			} else {
				// Still running, allow context switch
				time.Sleep(time.Millisecond)
			}
		}
	}

	// Unblock the worker. It should finish Task 1 and then see the stopped flag.
	blockerWg.Done()

	// Wait for Stop() to return
	<-stopDone

	if atomic.LoadInt64(&task2Ran) == 1 {
		t.Error("Task 2 should have been discarded")
	}
}


