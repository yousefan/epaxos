package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Replica represents a single EPaxos node in the cluster
type Replica struct {
	ID           ReplicaID                       // Unique ID for this replica
	Peers        []string                        // Addresses of other replicas
	Instances    map[int]map[int]*EPaxosInstance // [replicaID][instanceID] => EPaxosInstance
	InstanceLock sync.RWMutex                    // Protects Instances map

	NextInstance int // Next available instance slot
	KVStore      *KVStore
}

// NewReplica creates a new replica with the given ID and peers
func NewReplica(id ReplicaID, peers []string) *Replica {
	if peers == nil {
		peers = []string{}
	}
	return &Replica{
		ID:           id,
		Peers:        peers,
		Instances:    make(map[int]map[int]*EPaxosInstance),
		NextInstance: 0,
		KVStore:      NewKVStore(),
	}
}

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

// TryExecute attempts to execute a committed command following the EPaxos execution algorithm
func (r *Replica) TryExecute(replicaID int, instanceID int) bool {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	// Check if instance exists and is in the right state
	instanceMap, ok := r.Instances[replicaID]
	if !ok {
		// Instance map doesn't exist - try to recover
		GetLogger().Warn(EXECUTION, "Instance map for replica %d not found, attempting recovery", replicaID)
		go r.RecoverInstance(replicaID, instanceID)
		return false
	}

	inst, ok := instanceMap[instanceID]
	if !ok {
		// Instance doesn't exist - try to recover
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

	// Step 1: Build dependency graph (lock already held)
	graph := r.BuildDependencyGraph(replicaID, instanceID)

	// Check if the target instance is in the graph
	targetKey := makeKey(replicaID, instanceID)
	if _, exists := graph.Nodes[targetKey]; !exists {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d not found in dependency graph", replicaID, instanceID)
		return false
	}

	// Step 2: Find strongly connected components
	sccs := graph.StronglyConnectedComponents()

	// Step 3: Locate the SCC containing the target instance
	targetKey = makeKey(replicaID, instanceID)
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

	// Step 4: Ensure all external dependencies of the target SCC are executed
	inSCC := make(map[string]bool, len(targetSCC))
	for _, k := range targetSCC {
		inSCC[k] = true
	}
	for _, k := range targetSCC {
		node := graph.Nodes[k]
		for _, dep := range node.Instance.Deps {
			depKey := makeKey(dep.ReplicaID, dep.InstanceID)
			if inSCC[depKey] {
				continue // internal edge
			}
			if depMap, ok := r.Instances[dep.ReplicaID]; !ok {
				GetLogger().Debug(EXECUTION, "External dependency R%d.%d: replica map missing", dep.ReplicaID, dep.InstanceID)
				// attempt recovery and bail
				go r.RecoverInstance(dep.ReplicaID, dep.InstanceID)
				return false
			} else if depInst, ok := depMap[dep.InstanceID]; !ok || !depInst.Executed {
				GetLogger().Debug(EXECUTION, "External dependency R%d.%d not executed yet", dep.ReplicaID, dep.InstanceID)
				return false
			}
		}
	}

	// Step 5: Execute commands in the SCC containing the target, ordered by (Seq, ReplicaID, InstanceID)
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

	if !executed {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d was not executed in any SCC", replicaID, instanceID)
		return false
	}

	return true
}

func (r *Replica) executeCommand(replicaID, instanceID int, inst *EPaxosInstance) bool {

	startTime := time.Now()

	if inst.Executed {
		return true
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
		// Log the error but still consider the command as successfully executed
		// For GET operations, "key not found" is a valid result, not a failure
		LogExecutionFailure(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, err)
		// You might want to store the error as the result for client response
	} else {
		LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, inst.CommandID, result, totalDuration)
	}

	return true
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
