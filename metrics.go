package main

import (
	"sync/atomic"
)

// Global metrics counters (thread-safe using atomic operations)
var (
	totalRequestsCount  int64
	fastPathCount       int64
	slowPathCount       int64
	conflictDetectCount int64
)

// IncrementTotalRequests atomically increments the total requests counter
func IncrementTotalRequests() {
	atomic.AddInt64(&totalRequestsCount, 1)
}

// IncrementFastPath atomically increments the fast path counter
func IncrementFastPath() {
	atomic.AddInt64(&fastPathCount, 1)
}

// IncrementSlowPath atomically increments the slow path counter
func IncrementSlowPath() {
	atomic.AddInt64(&slowPathCount, 1)
}

// IncrementConflictDetect atomically increments the conflict detection counter
func IncrementConflictDetect() {
	atomic.AddInt64(&conflictDetectCount, 1)
}

// GetMetrics returns the current values of all counters
func GetMetrics() (totalRequests, fastPath, slowPath, conflictDetect int64) {
	return atomic.LoadInt64(&totalRequestsCount),
		atomic.LoadInt64(&fastPathCount),
		atomic.LoadInt64(&slowPathCount),
		atomic.LoadInt64(&conflictDetectCount)
}

// ResetMetrics resets all counters to zero
func ResetMetrics() {
	atomic.StoreInt64(&totalRequestsCount, 0)
	atomic.StoreInt64(&fastPathCount, 0)
	atomic.StoreInt64(&slowPathCount, 0)
	atomic.StoreInt64(&conflictDetectCount, 0)
}

// MetricsRequest is the request structure for getting metrics
type MetricsRequest struct {
	Reset bool // If true, reset counters after reading
}

// MetricsReply contains the current metric values
type MetricsReply struct {
	TotalRequests int64
	FastPathCount int64
	SlowPathCount int64
	ConflictCount int64
}
