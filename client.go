package main

import (
	"fmt"
	"math/rand"
	"net/rpc"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ===== Types copied from your template =====

type CommandType int

const (
	CmdGet CommandType = iota
	CmdPut
)

func (ct CommandType) String() string {
	switch ct {
	case CmdGet:
		return "GET"
	case CmdPut:
		return "PUT"
	default:
		return "UNKNOWN"
	}
}

type Command struct {
	Type  CommandType
	Key   string
	Value string
}

type CommandID struct {
	ClientID string
	SeqNum   int
}

type ClientRequest struct {
	Command      Command
	CommandID    CommandID
	CommandCount int
}

type ClientReply struct {
	Success bool
	Value   string
	Error   string
}

// ===== Client RPC helper (fills in missing SeqNum) =====

var globalSeq int64 // monotonic across the process

func SendClientCommand(address string, cmd Command, commandCount int) (*ClientReply, time.Duration, error) {
	startTime := time.Now()

	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, time.Since(startTime), fmt.Errorf("failed to connect to replica: %w", err)
	}
	defer client.Close()

	cmdID := CommandID{
		ClientID: fmt.Sprintf("client_%d", rand.Intn(10000)),
		SeqNum:   int(atomic.AddInt64(&globalSeq, 1)),
	}

	req := ClientRequest{
		Command:      cmd,
		CommandID:    cmdID,
		CommandCount: commandCount,
	}
	var reply ClientReply

	err = client.Call("ReplicaRPC.ClientPropose", req, &reply)
	duration := time.Since(startTime)
	if err != nil {
		return nil, duration, fmt.Errorf("RPC call failed: %w", err)
	}
	return &reply, duration, nil
}

// ===== Load generator =====

func main() {
	// --- Hardcoded config (tweak as needed) ---
	const (
		testDuration = 1 * time.Second
		concurrency  = 5
		conflictProb = 0.25 // ≈50% of requests use the shared key -> ~50% conflicts
		sharedKey    = "share_key"
		valueBytes   = 16 // payload size (arbitrary)
	)
	replicas := []string{
		"localhost:8000",
		"localhost:8001",
		"localhost:8002",
		"localhost:8003",
		"localhost:8004",
	}

	rand.Seed(time.Now().UnixNano())

	// Metrics
	var totalRequests int64
	var totalSuccess int64
	latCh := make(chan time.Duration, 100000) // ample buffer
	errCh := make(chan struct{}, 100000)

	// Collector for latencies
	var allLats []time.Duration
	var collectWG sync.WaitGroup
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for d := range latCh {
			allLats = append(allLats, d)
		}
	}()

	// Spawn clients
	var wg sync.WaitGroup
	deadline := time.Now().Add(testDuration)

	for c := 0; c < concurrency; c++ {
		wg.Add(1)
		go func(clientIdx int) {
			defer wg.Done()

			// Per-goroutine RNG
			src := rand.NewSource(time.Now().UnixNano() + int64(clientIdx)*1_000_003)
			r := rand.New(src)

			localSeq := 0
			for time.Now().Before(deadline) {
				localSeq++

				// Pick key: shared ~p, otherwise client-unique key to avoid cross-client collisions
				key := sharedKey
				if r.Float64() >= conflictProb {
					key = fmt.Sprintf("key_c%d_%d", clientIdx, localSeq)
				}

				// Random value
				val := randString(r, valueBytes)

				// Round-robin target replica to spread load
				addr := replicas[localSeq%len(replicas)]

				cmd := Command{
					Type:  CmdPut,
					Key:   key,
					Value: val,
				}

				reqNum := int(atomic.AddInt64(&totalRequests, 1))

				reply, dur, err := SendClientCommand(addr, cmd, reqNum)
				latCh <- dur

				if err == nil && reply != nil && reply.Success {
					atomic.AddInt64(&totalSuccess, 1)
				} else {
					errCh <- struct{}{}
				}
			}
		}(c)
	}

	wg.Wait()
	close(latCh)
	collectWG.Wait()

	close(errCh)
	var totalErrors int64
	for range errCh {
		totalErrors++
	}

	// Summarize
	elapsed := testDuration // by construction
	success := atomic.LoadInt64(&totalSuccess)
	requests := atomic.LoadInt64(&totalRequests)
	tput := float64(success) / elapsed.Seconds()

	sort.Slice(allLats, func(i, j int) bool { return allLats[i] < allLats[j] })
	p := func(q float64) time.Duration {
		if len(allLats) == 0 {
			return 0
		}
		idx := int(float64(len(allLats)-1) * q)
		return allLats[idx]
	}
	var sum time.Duration
	for _, d := range allLats {
		sum += d
	}
	avg := time.Duration(0)
	if len(allLats) > 0 {
		avg = time.Duration(int64(sum) / int64(len(allLats)))
	}

	fmt.Println("=== EPaxos PUT load (conflict via shared key) ===")
	fmt.Printf("Replicas: %v\n", replicas)
	fmt.Printf("Concurrency: %d  Duration: %s  Shared-key prob: %.0f%%\n", concurrency, elapsed, conflictProb*100)
	fmt.Printf("Total Requests: %d  Success: %d  Errors: %d\n", requests, success, totalErrors)
	fmt.Printf("Throughput: %.1f ops/s (successful)\n", tput)
	if len(allLats) > 0 {
		fmt.Printf("Latency (success+fail): count=%d  avg=%s  p50=%s  p95=%s  p99=%s  max=%s\n",
			len(allLats), avg, p(0.50), p(0.95), p(0.99), allLats[len(allLats)-1])
	}
}

func randString(r *rand.Rand, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}
