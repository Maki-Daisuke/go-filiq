# filiq

[![Go Reference](https://pkg.go.dev/badge/github.com/Maki-Daisuke/go-filiq.svg)](https://pkg.go.dev/github.com/Maki-Daisuke/go-filiq)
[![Go Report Card](https://goreportcard.com/badge/github.com/Maki-Daisuke/go-filiq)](https://goreportcard.com/report/github.com/Maki-Daisuke/go-filiq)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

> **FI**FO + **LI**FO + **Q**ueue = **filiq**

filiq is a lightweight and dependency-free in-memory worker pool for Go.
It provides a unified interface for concurrent task processing with configurable **FIFO (Queue)** or **LIFO (Stack)** ordering.

## Features

- **🔀 Dual Mode:** Switch between `FIFO` (Queue) and `LIFO` (Stack) with a single config.
- **🛡️ Thread-Safe:** Fully synchronized for concurrent producers and consumers.
- **📦 Zero Dependencies:** Only uses the Go standard library.

## Installation

```bash
go get github.com/Maki-Daisuke/go-filiq

```

## Usage

### Basic FIFO (Queue) Usage

Standard First-In-First-Out processing. Useful for job queues, log processing, or event streams.

```go
package main

import (
  "fmt"
  "time"

  "github.com/Maki-Daisuke/go-filiq"
)

const NUM_OF_WORKERS = 4

func main() {
  // Create & start a new runner in FIFO mode (default)
  // You can pass options like filiq.WithWorkers(n)
  flq := filiq.New(filiq.WithWorkers(NUM_OF_WORKERS))
  defer flq.Stop() // Graceful shutdown

  // Add tasks
  for i := 1; i <= 5; i++ {
    flq.Put(func(){
      // TODO
    })
  }

  // Wait for tasks to drain (simple sleep for demo)
  time.Sleep(1 * time.Second)
}

```

### LIFO (Stack) Usage

Last-In-First-Out processing. Useful when the **freshest data matters most**, or when you want to handle the most recent user interaction immediately while older background tasks can wait.

```go
package main

import (
 "fmt"
 "github.com/Maki-Daisuke/go-filiq"
)

func main() {
  // LIFO mode with 4 workers
  flq := filiq.New(filiq.WithLIFO())
  defer flq.Stop() // Graceful shutdown

  // TODO
}
```

### Bounded Buffer Usage

You can limit the number of pending tasks to prevent memory issues. If the buffer is full, `Put()` will block until space is available.

```go
// Block Put() when 100 tasks are pending
flq := filiq.New(filiq.WithWorkers(5), filiq.WithBufferSize(100))
```

> [!NOTE]
> **Memory Usage Note:** While `WithBufferSize` strictly enforces the *logical* number of pending tasks, the *physical* memory allocated (slice capacity) may be up to ~2x the configured size. This is due to Go's slice growth strategy (doubling capacity on append) and `go-filiq`'s lazy compaction optimization.
>
> 💡 **Tip:** Setting `maxBuffer` to a **power of 2** (e.g., 1024, 4096) is recommended to align with Go's memory allocator and minimize unused capacity overhead.

## API Reference

### Functions

#### `New(opts ...filiq.Option) *filiq.Runner`

Creates a new `Runner` and starts worker goroutines.

- `opts`: Functional options to configure the runner.

#### Options

- `filiq.WithFIFO()`: Sets the mode to First-In-First-Out (Default).
- `filiq.WithLIFO()`: Sets the mode to Last-In-First-Out.
- `filiq.WithWorkers(n int)`: Sets the number of concurrent workers (default: 1).
- `filiq.WithBufferSize(size int)`: Sets the buffer size limit (default: unlimited).
- `filiq.WithPanicHandler(handler func(interface{}))`: Sets a callback to handle panics in tasks.
- `filiq.WithContext(ctx context.Context)`: Sets the context (default: `context.Background()`).

#### `(r *Runner) Put(task func()) bool`

Adds a task to the task queue. This method blocks if the buffer is full. If the runner is stopped, this operation is ignored and returns `false`.
This method is thread-safe.

#### `(r *Runner) TryPut(task func()) bool`

Attempts to add a task to the task queue. This method does not block. If the buffer is full or the runner is stopped, this operation is ignored and returns `false`.
This method is thread-safe.

#### `(r *Runner) Stop()`

Stops the runner.

- It signals all waiting workers to exit.
- It waits for all active workers to finish their current job before returning (Graceful Shutdown).
- Any tasks remaining in the buffer are discarded.

## Performance

Why use `filiq` over standard channels?

1. **LIFO Support:** Standard Go channels are strictly FIFO. Implementing LIFO usually requires complex mutex locking or inefficiency. `filiq` handles this natively.
2. **Flexible Buffering:** Unlike standard channels which always block or panic, `filiq` supports both **unbounded** (auto-growing) and **bounded** (fixed-size) buffers. By default, it grows automatically, ensuring producers truly never block locally.
3. **Condition Variables:** It uses `sync.Cond` for signaling. This avoids "busy loops" and ensures workers sleep efficiently when idle, waking up immediately when work arrives.

### Benchmarks

`filiq` is significantly faster than standard Go channels for worker pool patterns, thanks to reduced lock contention and `sync.Cond` efficiency.

**Environment:** Windows (AMD Ryzen 9 7950X)

| Implementation       | Type             | Time/Op        | Speedup   |
|----------------------|------------------|----------------|-----------|
| **Standard Channel** | FIFO (Buffered)  | ~98 ns/op      | 1.0x      |
| **filiq**            | FIFO (Bounded)   | **~82 ns/op**  | **1.20x** |
| **filiq**            | FIFO (Unbounded) | **~81 ns/op**  | **1.21x** |
| **filiq**            | LIFO (Bounded)   | **~85 ns/op**  | **1.15x** |

Measurements taken using `go test -bench . -benchmem`.

## License

MIT License

## Author

Copyright (c) 2026 Daisuke Maki
