package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"net/rpc"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ===== Types (same as before) =====

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

// ===== Persistent per-replica clients (NEW) =====

var (
	clientPool sync.Map // map[string]*rpc.Client, keyed by address
	globalSeq  int64    // monotonic across the process
)

func mustInitClients(replicas []string) {
	for _, addr := range replicas {
		c, err := rpc.Dial("tcp", addr)
		if err != nil {
			fmt.Printf("FATAL: failed to connect to replica %s: %v\n", addr, err)
			os.Exit(1)
		}
		clientPool.Store(addr, c)
	}
}

func closeClients() {
	clientPool.Range(func(key, value any) bool {
		if c, ok := value.(*rpc.Client); ok && c != nil {
			_ = c.Close()
		}
		return true
	})
}

func clientFor(addr string) (*rpc.Client, error) {
	if v, ok := clientPool.Load(addr); ok {
		if c, ok := v.(*rpc.Client); ok && c != nil {
			return c, nil
		}
	}
	// Should not happen because we eagerly dial, but keep a safety net:
	c, err := rpc.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	actual, _ := clientPool.LoadOrStore(addr, c)
	if actual != c {
		_ = c.Close()
		return actual.(*rpc.Client), nil
	}
	return c, nil
}

// ===== Send helper (reuses persistent client; no per-request Dial) =====

func SendClientCommand(address string, cmd Command, commandCount int) (*ClientReply, time.Duration, error) {
	startTime := time.Now()

	client, err := clientFor(address)
	if err != nil {
		return nil, time.Since(startTime), fmt.Errorf("failed to get client for %s: %w", address, err)
	}

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
	// --- Hardcoded config (unchanged) ---
	const (
		testDuration = 10 * time.Second
		concurrency  = 5
		conflictProb = 0.75
		sharedKey    = "share_key"
		valueBytes   = 16
	)
	replicas := []string{
		"localhost:8000",
		"localhost:8001",
		"localhost:8002",
		"localhost:8003",
		"localhost:8004",
	}

	rand.Seed(time.Now().UnixNano())

	// Init persistent clients (NEW)
	mustInitClients(replicas)
	defer closeClients()

	// Metrics
	var totalRequests int64
	var totalSuccess int64
	latCh := make(chan time.Duration, 100000)
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

	// ===== Progress timer (unchanged) =====
	startWall := time.Now()
	deadline := startWall.Add(testDuration)

	progressStop := make(chan struct{})
	var progressWG sync.WaitGroup
	progressWG.Add(1)
	go func() {
		defer progressWG.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				elapsed := time.Since(startWall)
				if elapsed > testDuration {
					elapsed = testDuration
				}
				remaining := testDuration - elapsed
				if remaining < 0 {
					remaining = 0
				}
				reqs := atomic.LoadInt64(&totalRequests)
				succ := atomic.LoadInt64(&totalSuccess)
				fmt.Printf("\r[run] elapsed %5.1fs / %5.1fs | remaining %5.1fs | sent=%d  success=%d",
					elapsed.Seconds(), testDuration.Seconds(), remaining.Seconds(), reqs, succ)
			case <-progressStop:
				elapsed := time.Since(startWall)
				if elapsed > testDuration {
					elapsed = testDuration
				}
				reqs := atomic.LoadInt64(&totalRequests)
				succ := atomic.LoadInt64(&totalSuccess)
				fmt.Printf("\r[run] elapsed %5.1fs / %5.1fs | sent=%d  success=%d\n",
					elapsed.Seconds(), testDuration.Seconds(), reqs, succ)
				return
			}
		}
	}()

	// Spawn clients
	var wg sync.WaitGroup
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

				// Pick key
				key := sharedKey
				if r.Float64() >= conflictProb {
					key = fmt.Sprintf("key_c%d_%d", clientIdx, localSeq)
				}

				// Random value
				val := randString(r, valueBytes)

				// Round-robin target replica
				addr := replicas[localSeq%len(replicas)]

				cmd := Command{Type: CmdPut, Key: key, Value: val}

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
	close(progressStop)
	progressWG.Wait()

	close(latCh)
	collectWG.Wait()

	close(errCh)
	var totalErrors int64
	for range errCh {
		totalErrors++
	}

	// Summarize
	elapsed := testDuration
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

	// Console output
	fmt.Println("=== EPaxos PUT load (conflict via shared key) ===")
	fmt.Printf("Replicas: %v\n", replicas)
	fmt.Printf("Concurrency: %d  Duration: %s  Shared-key prob: %.0f%%\n", concurrency, elapsed, conflictProb*100)
	fmt.Printf("Total Requests: %d  Success: %d  Errors: %d\n", requests, success, totalErrors)
	fmt.Printf("Throughput: %.1f ops/s (successful)\n", tput)
	if len(allLats) > 0 {
		fmt.Printf("Latency (success+fail): count=%d  avg=%s  p50=%s  p95=%s  p99=%s  max=%s\n",
			len(allLats), avg, p(0.50), p(0.95), p(0.99), allLats[len(allLats)-1])
	}

	// ===== CSV export (unchanged) =====
	confPct := int(conflictProb * 100)
	csvName := fmt.Sprintf("metrics_r%d_c%d_conf%d.csv", len(replicas), concurrency, confPct)

	ms := func(d time.Duration) string {
		return fmt.Sprintf("%.3f", float64(d)/float64(time.Millisecond))
	}

	file, err := os.Create(csvName)
	if err != nil {
		fmt.Printf("Failed to create CSV file %q: %v\n", csvName, err)
		return
	}
	defer file.Close()

	w := csv.NewWriter(file)
	defer w.Flush()

	_ = w.Write([]string{
		"replica_count",
		"concurrency",
		"conflict_prob",
		"duration_sec",
		"total_requests",
		"success",
		"errors",
		"throughput_ops_per_sec",
		"latency_count",
		"lat_avg_ms",
		"lat_p50_ms",
		"lat_p95_ms",
		"lat_p99_ms",
		"lat_max_ms",
	})

	var (
		p50  = p(0.50)
		p95  = p(0.95)
		p99  = p(0.99)
		lmax time.Duration
	)
	if len(allLats) > 0 {
		lmax = allLats[len(allLats)-1]
	}

	_ = w.Write([]string{
		fmt.Sprintf("%d", len(replicas)),
		fmt.Sprintf("%d", concurrency),
		fmt.Sprintf("%.2f", conflictProb),
		fmt.Sprintf("%.3f", elapsed.Seconds()),
		fmt.Sprintf("%d", requests),
		fmt.Sprintf("%d", success),
		fmt.Sprintf("%d", totalErrors),
		fmt.Sprintf("%.3f", tput),
		fmt.Sprintf("%d", len(allLats)),
		ms(avg),
		ms(p50),
		ms(p95),
		ms(p99),
		ms(lmax),
	})

	fmt.Printf("Saved CSV: %s\n", csvName)
}

func randString(r *rand.Rand, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}
