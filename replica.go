package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

type instKey struct{ rid, iid int }

type keyEntry struct {
	// Instances still relevant for conflicts on this key:
	writers map[string]instKey // PUTs (and any op treated as write)
	readers map[string]instKey // GETs
	// Optional speedups:
	maxSeq int            // max Seq among active instances on this key
	seqBy  map[string]int // instKey->Seq if you want exact max
}

// Replica represents a single EPaxos node in the cluster
type Replica struct {
	ID           ReplicaID                       // Unique ID for this replica
	Peers        []string                        // Addresses of other replicas
	Instances    map[int]map[int]*EPaxosInstance // [replicaID][instanceID] => EPaxosInstance
	InstanceLock sync.RWMutex                    // Protects Instances map

	NextInstance int // Next available instance slot
	KVStore      *KVStore

	readyCh       chan instKey         // bounded queue of runnable (or newly committed) instances
	pending       map[string]struct{}  // de-dupe keys already in the queue
	dependents    map[string][]instKey // reverse edges: X -> list that depend on X
	remainingDeps map[string]int       // (rid,iid) -> count of unexecuted deps
	keyIndex      map[string]*keyEntry

	// NEW: Bounded tracking and speculative execution support
	committedIndex   map[string]*keyEntry             // Index of only uncommitted instances for conflict detection
	speculativeState map[string]*SpeculativeExecution // Track speculative executions
	maxDependencyAge int64                            // Max age in milliseconds for dependency tracking (5 seconds)
}

// SpeculativeExecution tracks speculatively executed operations
type SpeculativeExecution struct {
	OriginalValue string
	NewValue      string
	Executed      bool
	Timestamp     time.Time
}

// NewReplica creates a new replica with the given ID and peers
func NewReplica(id ReplicaID, peers []string) *Replica {
	if peers == nil {
		peers = []string{}
	}
	return &Replica{
		ID:               id,
		Peers:            peers,
		Instances:        make(map[int]map[int]*EPaxosInstance),
		NextInstance:     0,
		KVStore:          NewKVStore(),
		readyCh:          make(chan instKey, 4096),
		pending:          make(map[string]struct{}),
		dependents:       make(map[string][]instKey),
		remainingDeps:    make(map[string]int),
		keyIndex:         make(map[string]*keyEntry),
		committedIndex:   make(map[string]*keyEntry),
		speculativeState: make(map[string]*SpeculativeExecution),
		maxDependencyAge: 5000, // 5 seconds
	}
}

// committedKe gets or creates a keyEntry in the committed index (for uncommitted instances only)
func (r *Replica) committedKe(key string) *keyEntry {
	e := r.committedIndex[key]
	if e == nil {
		e = &keyEntry{
			writers: make(map[string]instKey),
			readers: make(map[string]instKey),
			seqBy:   make(map[string]int),
		}
		r.committedIndex[key] = e
	}
	return e
}

func (r *Replica) ke(key string) *keyEntry {
	e := r.keyIndex[key]
	if e == nil {
		e = &keyEntry{
			writers: make(map[string]instKey),
			readers: make(map[string]instKey),
			seqBy:   make(map[string]int),
		}
		r.keyIndex[key] = e
	}
	return e
}

// indexAdd is called once when the instance becomes visible locally (on PreAccept/Accept/Commit save).
func (r *Replica) indexAdd(inst *EPaxosInstance, rid, iid int) {
	// Only add to index if not yet committed (for conflict detection)
	if !inst.Committed {
		e := r.committedKe(inst.Command.Key)
		k := ikey(rid, iid)
		if inst.Command.Type == CmdPut {
			e.writers[k] = instKey{rid, iid}
		} else {
			e.readers[k] = instKey{rid, iid}
		}
		// keep (approx) max sequence for quick bump
		e.seqBy[k] = inst.Seq
		if inst.Seq > e.maxSeq {
			e.maxSeq = inst.Seq
		}
	}
}

// indexRemoveFromCommitted removes an instance from the committed index when it gets committed
func (r *Replica) indexRemoveFromCommitted(cmd Command, rid, iid int) {
	e := r.committedIndex[cmd.Key]
	if e == nil {
		return
	}
	k := ikey(rid, iid)
	delete(e.writers, k)
	delete(e.readers, k)
	if s, ok := e.seqBy[k]; ok {
		delete(e.seqBy, k)
		if s >= e.maxSeq {
			// recompute lazily only when necessary
			e.maxSeq = 0
			for _, v := range e.seqBy {
				if v > e.maxSeq {
					e.maxSeq = v
				}
			}
		}
	}
	// Clean up empty entries
	if len(e.writers) == 0 && len(e.readers) == 0 {
		delete(r.committedIndex, cmd.Key)
	}
}

// indexRemove is called once after Execute completes.
func (r *Replica) indexRemove(cmd Command, rid, iid int) {
	e := r.keyIndex[cmd.Key]
	if e == nil {
		return
	}
	k := ikey(rid, iid)
	delete(e.writers, k)
	delete(e.readers, k)
	if s, ok := e.seqBy[k]; ok {
		delete(e.seqBy, k)
		if s >= e.maxSeq {
			// recompute lazily only when necessary
			e.maxSeq = 0
			for _, v := range e.seqBy {
				if v > e.maxSeq {
					e.maxSeq = v
				}
			}
		}
	}
	// Optional: if empty, delete the entry to keep memory tidy
	if len(e.writers) == 0 && len(e.readers) == 0 {
		delete(r.keyIndex, cmd.Key)
	}
}

func ikey(rid, iid int) string { return fmt.Sprintf("%d-%d", rid, iid) }

// DependencyGraph represents the dependency graph for execution
type DependencyGraph struct {
	Nodes map[string]*GraphNode
	Edges map[string][]string
}

type GraphNode struct {
	ReplicaID  int
	InstanceID int
	Instance   *EPaxosInstance
	Visited    bool
	InStack    bool
	Index      int
	LowLink    int
}

// NewDependencyGraph creates a new dependency graph
func NewDependencyGraph() *DependencyGraph {
	return &DependencyGraph{
		Nodes: make(map[string]*GraphNode),
		Edges: make(map[string][]string),
	}
}

// AddNode adds a node to the dependency graph
func (g *DependencyGraph) AddNode(replicaID, instanceID int, instance *EPaxosInstance) {
	key := makeKey(replicaID, instanceID)
	g.Nodes[key] = &GraphNode{
		ReplicaID:  replicaID,
		InstanceID: instanceID,
		Instance:   instance,
	}
	if g.Edges[key] == nil {
		g.Edges[key] = []string{}
	}
}

// AddEdge adds a directed edge from source to target
func (g *DependencyGraph) AddEdge(srcReplicaID, srcInstanceID, tgtReplicaID, tgtInstanceID int) {
	srcKey := makeKey(srcReplicaID, srcInstanceID)
	tgtKey := makeKey(tgtReplicaID, tgtInstanceID)

	// Only add edge if target exists
	if _, exists := g.Nodes[tgtKey]; exists {
		g.Edges[srcKey] = append(g.Edges[srcKey], tgtKey)
	}
}

// makeKey creates a unique key for a (replica, instance) pair
func makeKey(replicaID, instanceID int) string {
	return fmt.Sprintf("%d-%d", replicaID, instanceID)
}

// StronglyConnectedComponents finds all strongly connected components using Tarjan's algorithm
func (g *DependencyGraph) StronglyConnectedComponents() [][]string {
	var result [][]string
	var stack []string
	index := 0

	// Reset all nodes
	for _, node := range g.Nodes {
		node.Visited = false
		node.InStack = false
		node.Index = -1
		node.LowLink = -1
	}

	var tarjan func(string)
	tarjan = func(nodeKey string) {
		node := g.Nodes[nodeKey]
		node.Index = index
		node.LowLink = index
		node.InStack = true
		index++
		stack = append(stack, nodeKey)

		for _, neighborKey := range g.Edges[nodeKey] {
			neighbor := g.Nodes[neighborKey]
			if neighbor.Index == -1 {
				tarjan(neighborKey)
				if neighbor.LowLink < node.LowLink {
					node.LowLink = neighbor.LowLink
				}
			} else if neighbor.InStack {
				if neighbor.Index < node.LowLink {
					node.LowLink = neighbor.Index
				}
			}
		}

		// If node is a root, pop the stack and create an SCC
		if node.LowLink == node.Index {
			var scc []string
			for {
				wKey := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				g.Nodes[wKey].InStack = false
				scc = append(scc, wKey)
				if wKey == nodeKey {
					break
				}
			}
			result = append(result, scc)
		}
	}

	// Run Tarjan's algorithm for all unvisited nodes
	for nodeKey := range g.Nodes {
		if g.Nodes[nodeKey].Index == -1 {
			tarjan(nodeKey)
		}
	}

	return result
}

// BuildDependencyGraph creates the dependency graph for a command starting from the given instance
// NOTE: This method assumes the caller already holds the appropriate lock
func (r *Replica) BuildDependencyGraph(replicaID, instanceID int) *DependencyGraph {
	// FIXED: Removed lock acquisition since caller (TryExecute) already holds the lock
	// r.InstanceLock.RLock()
	// defer r.InstanceLock.RUnlock()

	graph := NewDependencyGraph()
	visited := make(map[string]bool)
	missingDependencies := make(map[string]bool)

	var buildRecursive func(int, int)
	buildRecursive = func(rid, iid int) {
		key := makeKey(rid, iid)
		if visited[key] {
			return
		}
		visited[key] = true

		// Get the instance
		instanceMap, exists := r.Instances[rid]
		if !exists {
			missingDependencies[key] = true
			GetLogger().Warn(EXECUTION, "Missing instance map for replica %d when building dependency graph", rid)
			return
		}
		instance, exists := instanceMap[iid]
		if !exists {
			missingDependencies[key] = true
			GetLogger().Warn(EXECUTION, "Missing instance R%d.%d when building dependency graph", rid, iid)
			return
		}
		if !instance.Committed {
			GetLogger().Debug(EXECUTION, "Instance R%d.%d not yet committed, skipping from dependency graph", rid, iid)
			return
		}

		// Add this node to the graph
		graph.AddNode(rid, iid, instance)

		// FIXED: Recurse first so the dependency node exists, then add the edge
		for _, dep := range instance.Deps {
			// Recurse first to ensure the dependency node exists, then add the edge
			buildRecursive(dep.ReplicaID, dep.InstanceID)
			graph.AddEdge(rid, iid, dep.ReplicaID, dep.InstanceID)
		}
	}

	buildRecursive(replicaID, instanceID)

	// If we found missing dependencies, trigger recovery for them
	if len(missingDependencies) > 0 {
		GetLogger().Info(EXECUTION, "Found %d missing dependencies, triggering recovery", len(missingDependencies))
		for depKey := range missingDependencies {
			// Parse the key back to replicaID and instanceID
			var depRid, depIid int
			fmt.Sscanf(depKey, "%d-%d", &depRid, &depIid)
			go r.RecoverInstance(depRid, depIid)
		}
	}

	return graph
}

// TryExecute attempts to execute a committed command with speculative execution support
func (r *Replica) TryExecute(replicaID int, instanceID int) bool {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	// Check if instance exists and is in the right state
	instanceMap, ok := r.Instances[replicaID]
	if !ok {
		GetLogger().Warn(EXECUTION, "Instance map for replica %d not found, attempting recovery", replicaID)
		go r.RecoverInstance(replicaID, instanceID)
		return false
	}

	inst, ok := instanceMap[instanceID]
	if !ok {
		GetLogger().Warn(EXECUTION, "Instance R%d.%d not found, attempting recovery", replicaID, instanceID)
		go r.RecoverInstance(replicaID, instanceID)
		return false
	}

	// Must be committed
	if !inst.Committed {
		GetLogger().Debug(EXECUTION, "Instance R%d.%d not yet committed", replicaID, instanceID)
		return false
	}

	// Already executed?
	if inst.Executed {
		GetLogger().Debug(EXECUTION, "Instance R%d.%d already executed", replicaID, instanceID)
		return true
	}

	LogExecutionAttempt(ReplicaID(replicaID), instanceID, inst)

	// Check for speculative execution opportunity
	canSpeculate := true
	hasUnresolvedDeps := false

	// Check dependencies with age limit
	currentTime := time.Now()
	for _, dep := range inst.Deps {
		// Skip old dependencies (bounded tracking)
		if depMap, ok := r.Instances[dep.ReplicaID]; ok {
			if depInst, ok := depMap[dep.InstanceID]; ok {
				// Check if dependency is too old (older than maxDependencyAge)
				if depInst.Timestamp.Time.Before(currentTime.Add(-time.Duration(r.maxDependencyAge) * time.Millisecond)) {
					continue // Skip old dependency
				}
				if !depInst.Executed {
					hasUnresolvedDeps = true
					// Don't speculate if critical dependencies are unresolved
					if depInst.Command.Key == inst.Command.Key {
						canSpeculate = false
						break
					}
				}
			}
		}
	}

	// Try speculative execution if safe
	if canSpeculate && hasUnresolvedDeps {
		r.speculativeExecute(replicaID, instanceID, inst)
		return false // Return false but mark as speculatively executed
	}

	// If can't speculate and has unresolved deps, wait
	if !canSpeculate && hasUnresolvedDeps {
		return false
	}

	// Normal execution path - build minimal dependency graph
	graph := r.BuildMinimalDependencyGraph(replicaID, instanceID)

	// Check if the target instance is in the graph
	targetKey := makeKey(replicaID, instanceID)
	if _, exists := graph.Nodes[targetKey]; !exists {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d not found in dependency graph", replicaID, instanceID)
		return false
	}

	// Find strongly connected components
	sccs := graph.StronglyConnectedComponents()

	// Locate the SCC containing the target instance
	var targetSCC []string
	for _, scc := range sccs {
		for _, k := range scc {
			if k == targetKey {
				targetSCC = scc
				break
			}
		}
		if targetSCC != nil {
			break
		}
	}
	if targetSCC == nil {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d not present in any SCC", replicaID, instanceID)
		return false
	}

	// Execute commands in the SCC
	executed := false
	var sccInstances []*GraphNode
	for _, k := range targetSCC {
		if node, exists := graph.Nodes[k]; exists {
			sccInstances = append(sccInstances, node)
		}
	}
	sort.Slice(sccInstances, func(i, j int) bool {
		if sccInstances[i].Instance.Seq != sccInstances[j].Instance.Seq {
			return sccInstances[i].Instance.Seq < sccInstances[j].Instance.Seq
		}
		if sccInstances[i].ReplicaID != sccInstances[j].ReplicaID {
			return sccInstances[i].ReplicaID < sccInstances[j].ReplicaID
		}
		return sccInstances[i].InstanceID < sccInstances[j].InstanceID
	})
	for _, node := range sccInstances {
		wasExecuted := r.executeCommand(node.ReplicaID, node.InstanceID, node.Instance)
		if node.ReplicaID == replicaID && node.InstanceID == instanceID {
			executed = wasExecuted
		}
	}

	return executed
}

// speculativeExecute performs speculative execution
func (r *Replica) speculativeExecute(replicaID, instanceID int, inst *EPaxosInstance) {
	if inst.Command.Type == CmdGet {
		// Don't speculate on reads
		return
	}

	key := inst.Command.Key
	specKey := makeKey(replicaID, instanceID)

	// Save original state for potential rollback
	originalValue, _ := r.KVStore.Get(key)

	spec := &SpeculativeExecution{
		OriginalValue: originalValue,
		NewValue:      inst.Command.Value,
		Executed:      true,
		Timestamp:     time.Now(),
	}
	r.speculativeState[specKey] = spec

	// Apply speculatively
	r.KVStore.Put(key, inst.Command.Value)

	GetLogger().Debug(EXECUTION, "Speculatively executed instance R%d.%d", replicaID, instanceID)
}

// rollbackSpeculative rolls back a speculative execution
func (r *Replica) rollbackSpeculative(replicaID, instanceID int, inst *EPaxosInstance) {
	specKey := makeKey(replicaID, instanceID)
	if spec, ok := r.speculativeState[specKey]; ok {
		if spec.Executed {
			r.KVStore.Put(inst.Command.Key, spec.OriginalValue)
			GetLogger().Debug(EXECUTION, "Rolled back speculative execution for instance R%d.%d", replicaID, instanceID)
		}
		delete(r.speculativeState, specKey)
	}
}

// executeCommand executes a command with speculative support
func (r *Replica) executeCommand(replicaID, instanceID int, inst *EPaxosInstance) bool {
	startTime := time.Now()

	if inst.Executed {
		return true
	}

	// Check if this was speculatively executed
	specKey := makeKey(replicaID, instanceID)
	if spec, ok := r.speculativeState[specKey]; ok {
		// Validate speculative execution
		if spec.NewValue == inst.Command.Value {
			// Speculation was correct, just mark as executed
			inst.Executed = true
			inst.Status = StatusExecuted
			delete(r.speculativeState, specKey)
			totalDuration := time.Since(startTime)
			LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, "speculative-success", totalDuration)
			return true
		} else {
			// Speculation was wrong, rollback
			r.rollbackSpeculative(replicaID, instanceID, inst)
		}
	}

	// Check if this is a no-op command
	if inst.Command.Key == "__noop__" {
		inst.Executed = true
		inst.Status = StatusExecuted
		totalDuration := time.Since(startTime)
		LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, "noop", totalDuration)
		return true
	}

	// Apply the command to the local KV store
	result, err := r.KVStore.ApplyCommand(inst.Command)

	inst.Executed = true
	inst.Status = StatusExecuted

	totalDuration := time.Since(startTime)

	if err != nil {
		LogExecutionFailure(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, err)
	} else {
		LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, result, totalDuration)
	}

	return true
}

// BuildMinimalDependencyGraph builds a minimal dependency graph with bounded scope
func (r *Replica) BuildMinimalDependencyGraph(replicaID, instanceID int) *DependencyGraph {
	graph := NewDependencyGraph()
	visited := make(map[string]bool)
	currentTime := time.Now()
	maxDepth := 10 // Limit recursion depth

	var buildRecursive func(int, int, int)
	buildRecursive = func(rid, iid, depth int) {
		if depth > maxDepth {
			return // Stop at max depth
		}

		key := makeKey(rid, iid)
		if visited[key] {
			return
		}
		visited[key] = true

		instanceMap, exists := r.Instances[rid]
		if !exists {
			return
		}
		instance, exists := instanceMap[iid]
		if !exists {
			return
		}
		if !instance.Committed {
			return
		}

		// Skip old instances (bounded tracking)
		if instance.Timestamp.Time.Before(currentTime.Add(-time.Duration(r.maxDependencyAge) * time.Millisecond)) {
			return
		}

		graph.AddNode(rid, iid, instance)

		for _, dep := range instance.Deps {
			// Check if dependency is recent enough
			if depMap, ok := r.Instances[dep.ReplicaID]; ok {
				if depInst, ok := depMap[dep.InstanceID]; ok {
					if !depInst.Timestamp.Time.Before(currentTime.Add(-time.Duration(r.maxDependencyAge) * time.Millisecond)) {
						buildRecursive(dep.ReplicaID, dep.InstanceID, depth+1)
						graph.AddEdge(rid, iid, dep.ReplicaID, dep.InstanceID)
					}
				}
			}
		}
	}

	buildRecursive(replicaID, instanceID, 0)
	return graph
}

// GetInstance safely retrieves an instance with proper error handling
func (r *Replica) GetInstance(replicaID, instanceID int) (*EPaxosInstance, bool) {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

	instanceMap, ok := r.Instances[replicaID]
	if !ok {
		GetLogger().Warn(EXECUTION, "Instance map for replica %d not found", replicaID)
		return nil, false
	}

	inst, ok := instanceMap[instanceID]
	if !ok {
		GetLogger().Warn(EXECUTION, "Instance R%d.%d not found", replicaID, instanceID)
		return nil, false
	}

	return inst, true
}

// SetInstance safely sets an instance with proper error handling
func (r *Replica) SetInstance(replicaID, instanceID int, inst *EPaxosInstance) {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	if _, exists := r.Instances[replicaID]; !exists {
		r.Instances[replicaID] = make(map[int]*EPaxosInstance)
	}

	r.Instances[replicaID][instanceID] = inst
}

// RecoverInstance attempts to recover a potentially missing/uncommitted instance.
// Safe to call from goroutines; it does quick checks and then invokes ExplicitPrepare.
func (r *Replica) RecoverInstance(replicaID, instanceID int) {
	GetLogger().Info(CONSENSUS, "Attempting to recover instance R%d.%d", replicaID, instanceID)

	// Small stagger to avoid a thundering herd if multiple replicas try to recover the same slot
	time.Sleep(time.Duration(r.ID) * 100 * time.Millisecond)

	// If it already exists and is committed, nothing to do.
	r.InstanceLock.RLock()
	if instanceMap, exists := r.Instances[replicaID]; exists {
		if inst, exists := instanceMap[instanceID]; exists && inst.Committed {
			r.InstanceLock.RUnlock()
			GetLogger().Debug(CONSENSUS, "Instance R%d.%d already recovered", replicaID, instanceID)
			return
		}
	}
	r.InstanceLock.RUnlock()

	// Kick off EPaxos recovery via Prepare → (re)Commit
	if err := r.ExplicitPrepare(replicaID, instanceID); err != nil {
		GetLogger().Error(CONSENSUS, "Failed to recover instance R%d.%d: %v", replicaID, instanceID, err)
	} else {
		GetLogger().Info(CONSENSUS, "Successfully initiated recovery for instance R%d.%d", replicaID, instanceID)
	}
}
