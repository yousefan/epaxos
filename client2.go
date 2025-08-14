package main

import (
	"encoding/csv"
	"fmt"
	"math/rand"
	"net/rpc"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

//
// ===== Types (unchanged; must match server) =====
//

type CommandType int

const (
	CmdGet CommandType = iota
	CmdPut
)

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

//
// ===== Tunables =====
//

const (
	testDuration         = 5 * time.Second // wall time for the run
	producers            = 2               // request generator goroutines
	valueBytes           = 16              // payload size
	conflictProb         = 0.01            // 0..1 (1.0 = always shared key)
	sharedKey            = "shared_key"
	perReplicaPoolSize   = 32 // TCP connections per replica
	perReplicaInFlight   = 32 // outstanding RPCs per replica
	progressEvery        = 1 * time.Second
	randomizeReplicaPick = true // true=random, false=round-robin
)

// Target replicas (host networking; your server listens on :8000)
var replicas = []string{
	"localhost:8000",
	"localhost:8001",
	"localhost:8002",
	"localhost:8003",
	"localhost:8004",
}

//
// ===== Connection pool per replica =====
//

type rpcPool struct {
	addr    string
	clients []*rpc.Client
	next    uint32
}

func newRPCPool(addr string, n int) *rpcPool {
	p := &rpcPool{addr: addr, clients: make([]*rpc.Client, n)}
	for i := range p.clients {
		c, err := rpc.Dial("tcp", addr)
		if err != nil {
			panic(fmt.Errorf("dial %s: %w", addr, err))
		}
		p.clients[i] = c
	}
	return p
}
func (p *rpcPool) get() *rpc.Client {
	i := atomic.AddUint32(&p.next, 1)
	return p.clients[int(i)%len(p.clients)]
}
func (p *rpcPool) close() {
	for _, c := range p.clients {
		_ = c.Close()
	}
}

//
// ===== In-flight limiter per replica =====
//

type limiter struct{ ch chan struct{} }

func newLimiter(n int) limiter       { return limiter{ch: make(chan struct{}, n)} }
func (l limiter) acquire()           { l.ch <- struct{}{} }
func (l limiter) release()           { <-l.ch }
func (l limiter) drainingCount() int { return len(l.ch) }

//
// ===== Global state =====
//

var (
	globalSeq     int64
	totalSent     int64
	totalDone     int64
	totalSuccess  int64
	totalErrors   int64
	latCh         = make(chan time.Duration, 1<<20) // big buffer for latencies
	doneCh        = make(chan *rpc.Call, 1<<20)     // shared completion channel
	stopProducers int32
)

type callMeta struct {
	start time.Time
	ridx  int
	reply *ClientReply
}

// map[*rpc.Call] -> callMeta (concurrent)
var meta sync.Map

//
// ===== Helpers =====
//

func randString(r *rand.Rand, n int) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alpha[r.Intn(len(alpha))]
	}
	return string(b)
}

// Structure to hold metrics for each replica
type ReplicaMetrics struct {
	ReplicaID     string
	TotalRequests int64
	FastPathCount int64
	SlowPathCount int64
	ConflictCount int64
	FastPathPct   float64
	SlowPathPct   float64
	ConflictPct   float64
}

//
// ===== Main =====
//

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Build pools and limiters
	pools := make([]*rpcPool, len(replicas))
	limits := make([]limiter, len(replicas))
	for i, addr := range replicas {
		pools[i] = newRPCPool(addr, perReplicaPoolSize)
		limits[i] = newLimiter(perReplicaInFlight)
	}
	defer func() {
		for _, p := range pools {
			p.close()
		}
	}()

	// Latency collector
	var (
		collectWG sync.WaitGroup
		allLats   []time.Duration
	)
	collectWG.Add(1)
	go func() {
		defer collectWG.Done()
		for d := range latCh {
			allLats = append(allLats, d)
		}
	}()

	// Completion harvester (single goroutine, super cheap)
	var harvestWG sync.WaitGroup
	harvestWG.Add(1)
	go func() {
		defer harvestWG.Done()
		for call := range doneCh {
			if v, ok := meta.LoadAndDelete(call); ok {
				m := v.(callMeta)
				latCh <- time.Since(m.start)
				if call.Error == nil && m.reply != nil && m.reply.Success {
					atomic.AddInt64(&totalSuccess, 1)
				} else {
					atomic.AddInt64(&totalErrors, 1)
				}
				limits[m.ridx].release()
			}
			atomic.AddInt64(&totalDone, 1)
		}
	}()

	// Progress meter
	startWall := time.Now()
	deadline := startWall.Add(testDuration)
	var progWG sync.WaitGroup
	progWG.Add(1)
	go func() {
		defer progWG.Done()
		t := time.NewTicker(progressEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				elapsed := time.Since(startWall)
				if elapsed > testDuration {
					elapsed = testDuration
				}
				remaining := testDuration - elapsed
				if remaining < 0 {
					remaining = 0
				}
				fmt.Printf("\r[run] elapsed %5.1fs / %5.1fs | remaining %5.1fs | sent=%d done=%d ok=%d err=%d",
					elapsed.Seconds(), testDuration.Seconds(), remaining.Seconds(),
					atomic.LoadInt64(&totalSent), atomic.LoadInt64(&totalDone),
					atomic.LoadInt64(&totalSuccess), atomic.LoadInt64(&totalErrors))
			default:
				if atomic.LoadInt32(&stopProducers) == 1 && atomic.LoadInt64(&totalDone) >= atomic.LoadInt64(&totalSent) {
					fmt.Printf("\n")
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	// Producers
	var prodWG sync.WaitGroup
	for p := 0; p < producers; p++ {
		prodWG.Add(1)
		go func(pid int) {
			defer prodWG.Done()
			r := rand.New(rand.NewSource(time.Now().UnixNano() + int64(pid)*1_000_003))
			localSeq := 0
			var rr uint64
			for {
				now := time.Now()
				if now.After(deadline) {
					return
				}

				// Choose replica
				var ridx int
				if randomizeReplicaPick {
					ridx = r.Intn(len(replicas))
				} else {
					ridx = int(atomic.AddUint64(&rr, 1)) % len(replicas)
				}

				// Backpressure
				limits[ridx].acquire()

				// Prepare request
				localSeq++
				key := sharedKey
				if r.Float64() >= conflictProb {
					key = fmt.Sprintf("key_p%d_%d", pid, localSeq)
				}
				val := randString(r, valueBytes)

				req := &ClientRequest{
					Command: Command{
						Type:  CmdPut,
						Key:   key,
						Value: val,
					},
					CommandID: CommandID{
						ClientID: fmt.Sprintf("cli_%d", pid),
						SeqNum:   int(atomic.AddInt64(&globalSeq, 1)),
					},
					CommandCount: int(atomic.AddInt64(&totalSent, 1) + 1), // just a monotonic tag
				}

				reply := new(ClientReply) // unique per call

				// Async call on pooled client; use shared doneCh
				call := pools[ridx].get().Go("ReplicaRPC.ClientPropose", req, reply, doneCh)

				// Record metadata for completion
				meta.Store(call, callMeta{start: now, ridx: ridx, reply: reply})
			}
		}(p)
	}

	// Wait for producers, then stop and drain
	prodWG.Wait()
	atomic.StoreInt32(&stopProducers, 1)

	// Spin until all inflight calls complete
	for {
		if atomic.LoadInt64(&totalDone) >= atomic.LoadInt64(&totalSent) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Close channels and finish
	close(doneCh)
	harvestWG.Wait()
	close(latCh)
	collectWG.Wait()
	progWG.Wait()

	// Summarize
	elapsed := time.Since(startWall)
	success := atomic.LoadInt64(&totalSuccess)
	reqs := atomic.LoadInt64(&totalSent)
	errs := atomic.LoadInt64(&totalErrors)

	throughput := float64(reqs) / elapsed.Seconds()      // send rate
	throughputOK := float64(success) / elapsed.Seconds() // success rate

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

	fmt.Println("=== EPaxos PUT load (async net/rpc) ===")
	fmt.Printf("Replicas: %v\n", replicas)
	fmt.Printf("Producers: %d  Pool/replica: %d  InFlight/replica: %d  Duration: %s\n",
		producers, perReplicaPoolSize, perReplicaInFlight, testDuration)
	fmt.Printf("Sent: %d  Done: %d  Success: %d  Errors: %d\n", reqs, atomic.LoadInt64(&totalDone), success, errs)
	fmt.Printf("Send rate: %.1f ops/s   Success rate: %.1f ops/s\n", throughput, throughputOK)
	if len(allLats) > 0 {
		fmt.Printf("Latency (all): count=%d  avg=%s  p50=%s  p95=%s  p99=%s  max=%s\n",
			len(allLats), avg, p(0.50), p(0.95), p(0.99), allLats[len(allLats)-1])
	}

	// ===== Collect server metrics before saving CSV =====
	clusterMetrics, perReplicaMetrics := collectServerMetrics()

	// ===== Combined CSV export with cluster metrics =====
	saveCombinedCSV(allLats, avg, p(0.50), p(0.95), p(0.99), elapsed, reqs, success, errs, throughputOK, clusterMetrics)

	// ===== Save per-replica metrics separately =====
	savePerReplicaMetrics(perReplicaMetrics)
}

func saveCombinedCSV(allLats []time.Duration, avg, p50, p95, p99 time.Duration,
	elapsed time.Duration, totalReq, success, errs int64, tputOK float64,
	clusterMetrics MetricsReply) {

	csvName := fmt.Sprintf("cluster_metrics__r%d_producers%d_pool%d_inflight%d.csv",
		len(replicas), producers, perReplicaPoolSize, perReplicaInFlight)

	ms := func(d time.Duration) string {
		return fmt.Sprintf("%.3f", float64(d)/float64(time.Millisecond))
	}

	file, err := os.Create(csvName)
	if err != nil {
		fmt.Printf("Failed to create CSV %q: %v\n", csvName, err)
		return
	}
	defer file.Close()

	w := csv.NewWriter(file)
	defer w.Flush()

	// Calculate cluster percentages
	var clusterFastPct, clusterSlowPct, clusterConflictPct float64
	if clusterMetrics.TotalRequests > 0 {
		clusterFastPct = float64(clusterMetrics.FastPathCount) * 100.0 / float64(clusterMetrics.TotalRequests)
		clusterSlowPct = float64(clusterMetrics.SlowPathCount) * 100.0 / float64(clusterMetrics.TotalRequests)
		clusterConflictPct = float64(clusterMetrics.ConflictCount) * 100.0 / float64(clusterMetrics.TotalRequests)
	}

	// Combined header with both performance and server metrics
	_ = w.Write([]string{
		// Original performance metrics
		"replica_count",
		"producers",
		"pool_per_replica",
		"inflight_per_replica",
		"duration_sec",
		"total_sent",
		"success",
		"errors",
		"throughput_success_ops_s",
		"latency_count",
		"lat_avg_ms",
		"lat_p50_ms",
		"lat_p95_ms",
		"lat_p99_ms",
		"lat_max_ms",
		// Server cluster metrics
		"server_total_requests",
		"server_fast_path_count",
		"server_slow_path_count",
		"server_conflict_count",
		"server_fast_path_pct",
		"server_slow_path_pct",
		"server_conflict_pct",
	})

	var lmax time.Duration
	if len(allLats) > 0 {
		lmax = allLats[len(allLats)-1]
	}

	// Combined data row
	_ = w.Write([]string{
		// Original performance metrics
		fmt.Sprintf("%d", len(replicas)),
		fmt.Sprintf("%d", producers),
		fmt.Sprintf("%d", perReplicaPoolSize),
		fmt.Sprintf("%d", perReplicaInFlight),
		fmt.Sprintf("%.3f", elapsed.Seconds()),
		fmt.Sprintf("%d", totalReq),
		fmt.Sprintf("%d", success),
		fmt.Sprintf("%d", errs),
		fmt.Sprintf("%.3f", tputOK),
		fmt.Sprintf("%d", len(allLats)),
		ms(avg),
		ms(p50),
		ms(p95),
		ms(p99),
		ms(lmax),
		// Server cluster metrics
		fmt.Sprintf("%d", clusterMetrics.TotalRequests),
		fmt.Sprintf("%d", clusterMetrics.FastPathCount),
		fmt.Sprintf("%d", clusterMetrics.SlowPathCount),
		fmt.Sprintf("%d", clusterMetrics.ConflictCount),
		fmt.Sprintf("%.2f", clusterFastPct),
		fmt.Sprintf("%.2f", clusterSlowPct),
		fmt.Sprintf("%.2f", clusterConflictPct),
	})

	fmt.Printf("Saved combined metrics CSV: %s\n", csvName)
}

func collectServerMetrics() (MetricsReply, []ReplicaMetrics) {
	fmt.Println("\n=== Collecting Server Metrics ===")

	var replicaMetrics []ReplicaMetrics
	var clusterTotal MetricsReply

	// Collect metrics from each replica
	for i, addr := range replicas {
		fmt.Printf("Getting metrics from replica %s...\n", addr)

		// Create a new connection for metrics (don't use the pool)
		client, err := rpc.Dial("tcp", addr)
		if err != nil {
			fmt.Printf("Failed to connect to replica %s: %v\n", addr, err)
			continue
		}
		defer client.Close()

		req := MetricsRequest{Reset: false} // Don't reset, just read
		var reply MetricsReply

		err = client.Call("ReplicaRPC.GetMetrics", req, &reply)
		if err != nil {
			fmt.Printf("Failed to get metrics from replica %s: %v\n", addr, err)
			continue
		}

		// Calculate percentages
		var fastPct, slowPct, conflictPct float64
		if reply.TotalRequests > 0 {
			fastPct = float64(reply.FastPathCount) * 100.0 / float64(reply.TotalRequests)
			slowPct = float64(reply.SlowPathCount) * 100.0 / float64(reply.TotalRequests)
			conflictPct = float64(reply.ConflictCount) * 100.0 / float64(reply.TotalRequests)
		}

		// Store metrics for this replica
		replicaMetrics = append(replicaMetrics, ReplicaMetrics{
			ReplicaID:     fmt.Sprintf("replica_%d", i),
			TotalRequests: reply.TotalRequests,
			FastPathCount: reply.FastPathCount,
			SlowPathCount: reply.SlowPathCount,
			ConflictCount: reply.ConflictCount,
			FastPathPct:   fastPct,
			SlowPathPct:   slowPct,
			ConflictPct:   conflictPct,
		})

		// Add to cluster totals
		clusterTotal.TotalRequests += reply.TotalRequests
		clusterTotal.FastPathCount += reply.FastPathCount
		clusterTotal.SlowPathCount += reply.SlowPathCount
		clusterTotal.ConflictCount += reply.ConflictCount

		fmt.Printf("  Replica %d: Total=%d, FastPath=%d (%.2f%%), SlowPath=%d (%.2f%%), Conflicts=%d (%.2f%%)\n",
			i, reply.TotalRequests, reply.FastPathCount, fastPct,
			reply.SlowPathCount, slowPct, reply.ConflictCount, conflictPct)
	}

	// Print cluster summary
	var clusterFastPct, clusterSlowPct, clusterConflictPct float64
	if clusterTotal.TotalRequests > 0 {
		clusterFastPct = float64(clusterTotal.FastPathCount) * 100.0 / float64(clusterTotal.TotalRequests)
		clusterSlowPct = float64(clusterTotal.SlowPathCount) * 100.0 / float64(clusterTotal.TotalRequests)
		clusterConflictPct = float64(clusterTotal.ConflictCount) * 100.0 / float64(clusterTotal.TotalRequests)
	}

	fmt.Println("\n=== Cluster Totals ===")
	fmt.Printf("Total Requests: %d\n", clusterTotal.TotalRequests)
	fmt.Printf("Fast Path: %d (%.2f%%)\n", clusterTotal.FastPathCount, clusterFastPct)
	fmt.Printf("Slow Path: %d (%.2f%%)\n", clusterTotal.SlowPathCount, clusterSlowPct)
	fmt.Printf("Conflicts: %d (%.2f%%)\n", clusterTotal.ConflictCount, clusterConflictPct)

	return clusterTotal, replicaMetrics
}

func savePerReplicaMetrics(replicaMetrics []ReplicaMetrics) {
	// Save per-replica metrics CSV
	perReplicaCSV := fmt.Sprintf("per_replica_metrics_r%d_producers%d.csv",
		len(replicas), producers)

	file, err := os.Create(perReplicaCSV)
	if err != nil {
		fmt.Printf("Failed to create per-replica CSV %q: %v\n", perReplicaCSV, err)
		return
	}
	defer file.Close()

	w := csv.NewWriter(file)
	defer w.Flush()

	// Write header for per-replica CSV
	_ = w.Write([]string{
		"replica_id",
		"total_requests",
		"fast_path_count",
		"slow_path_count",
		"conflict_count",
		"fast_path_pct",
		"slow_path_pct",
		"conflict_pct",
	})

	// Write data for each replica
	for _, metrics := range replicaMetrics {
		_ = w.Write([]string{
			metrics.ReplicaID,
			fmt.Sprintf("%d", metrics.TotalRequests),
			fmt.Sprintf("%d", metrics.FastPathCount),
			fmt.Sprintf("%d", metrics.SlowPathCount),
			fmt.Sprintf("%d", metrics.ConflictCount),
			fmt.Sprintf("%.2f", metrics.FastPathPct),
			fmt.Sprintf("%.2f", metrics.SlowPathPct),
			fmt.Sprintf("%.2f", metrics.ConflictPct),
		})
	}

	fmt.Printf("Saved per-replica metrics CSV: %s\n", perReplicaCSV)
}
