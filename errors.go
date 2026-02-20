package filiq

import "errors"

var (
	// ErrQueueFull is returned when a task is submitted to a full queue in bounded mode.
	ErrQueueFull = errors.New("filiq: queue is full")

	// ErrQueueClosed is returned when a task is submitted to a closed (shutdown) runner.
	ErrQueueClosed = errors.New("filiq: queue is closed")
)
