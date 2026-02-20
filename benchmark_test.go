package filiq_test

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/Maki-Daisuke/go-filiq"
)

// BenchmarkStandardChannel simulates a standard worker pool using native channels
func BenchmarkStandardChannel(b *testing.B) {
	workers := 4
	// Use buffered channel for fair comparison with buffered filiq
	tasks := make(chan func(), 64)
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < workers; i++ {
		go func() {
			for task := range tasks {
				task()
			}
		}()
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wg.Add(1)
		tasks <- func() {
			wg.Done()
		}
	}
	wg.Wait()
	close(tasks)
}

// BenchmarkFiliqBounded benchmarks bounded buffer performance
func BenchmarkFiliqBounded(b *testing.B) {
	workers := 4
	// Use same buffer size as standard channel
	r := filiq.New(filiq.WithWorkers(workers), filiq.WithBufferSize(64))
	defer r.Shutdown(context.Background())

	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		for {
			err := r.Submit(func() {
				wg.Done()
			})
			if err == nil {
				break
			}
			runtime.Gosched()
		}
	}
	wg.Wait()
}

// BenchmarkFiliqUnbounded benchmarks unbounded buffer performance
func BenchmarkFiliqUnbounded(b *testing.B) {
	workers := 4
	// Default is unbounded
	r := filiq.New(filiq.WithWorkers(workers))
	defer r.Shutdown(context.Background())

	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		r.Submit(func() {
			wg.Done()
		})
	}
	wg.Wait()
}

// BenchmarkFiliqLIFOUnbounded benchmarks the LIFO mode (Unbounded)
func BenchmarkFiliqLIFOUnbounded(b *testing.B) {
	workers := 4
	r := filiq.New(filiq.WithWorkers(workers), filiq.WithLIFO())
	defer r.Shutdown(context.Background())

	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		r.Submit(func() {
			wg.Done()
		})
	}
	wg.Wait()
}

// BenchmarkFiliqLIFOBounded benchmarks the LIFO mode (Bounded)
func BenchmarkFiliqLIFOBounded(b *testing.B) {
	workers := 4
	r := filiq.New(filiq.WithWorkers(workers), filiq.WithLIFO(), filiq.WithBufferSize(64))
	defer r.Shutdown(context.Background())

	var wg sync.WaitGroup
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		wg.Add(1)
		for {
			err := r.Submit(func() {
				wg.Done()
			})
			if err == nil {
				break
			}
			runtime.Gosched()
		}
	}
	wg.Wait()
}
