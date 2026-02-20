package filiq_test

import (
	"context"
	"testing"
	"time"

	"github.com/Maki-Daisuke/go-filiq"
)

func TestPanicRecovery(t *testing.T) {
	var capturedPanic interface{}
	// 1 worker, and register a panic handler
	r := filiq.New(filiq.WithWorkers(1), filiq.WithPanicHandler(func(p interface{}) {
		capturedPanic = p
	}))
	defer r.Shutdown(context.Background())

	// Task that panics
	r.Submit(func() {
		panic("oops")
	})

	// Brief sleep to let worker pick up and panic
	time.Sleep(50 * time.Millisecond)

	// Verify handler was called
	if capturedPanic == nil {
		t.Error("expected panic handler to be called, but it wasn't")
	} else if val, ok := capturedPanic.(string); !ok || val != "oops" {
		t.Errorf("expected panic value 'oops', got %v", capturedPanic)
	}

	// Put normal task
	done := make(chan struct{})
	err := r.Submit(func() {
		close(done)
	})
	if err != nil {
		t.Fatalf("Failed to put task: %v", err)
	}

	select {
	case <-done:
		// Worker survived or restarted
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Worker died after panic, subsequent task not processed")
	}
}
