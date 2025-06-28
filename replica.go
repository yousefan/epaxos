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

	KVStore *KVStore // In-memory key-value store

	NextInstance int  // Local instance ID counter
	IsLeader     bool // Optional debug marker
}

// NewReplica creates and initializes a new replica
func NewReplica(id ReplicaID, peers []string) *Replica {
	return &Replica{
		ID:           id,
		Peers:        peers,
		Instances:    make(map[int]map[int]*EPaxosInstance),
		KVStore:      NewKVStore(),
		NextInstance: 0,
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

		// Visit all neighbors
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

		// If this is a root node, pop the stack and create SCC
		if node.LowLink == node.Index {
			var component []string
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				g.Nodes[top].InStack = false
				component = append(component, top)
				if top == nodeKey {
					break
				}
			}
			result = append(result, component)
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
func (r *Replica) BuildDependencyGraph(replicaID, instanceID int) *DependencyGraph {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

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

		// Add edges to all dependencies and visit them recursively
		for _, dep := range instance.Deps {
			graph.AddEdge(rid, iid, dep.ReplicaID, dep.InstanceID)
			buildRecursive(dep.ReplicaID, dep.InstanceID)
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

	if !inst.Committed {
		GetLogger().Debug(EXECUTION, "Instance R%d.%d not yet committed, cannot execute", replicaID, instanceID)
		return false
	}

	if inst.Executed {
		GetLogger().Debug(EXECUTION, "Instance R%d.%d already executed", replicaID, instanceID)
		return true
	}

	LogExecutionAttempt(ReplicaID(replicaID), instanceID, inst)

	// Check if all dependencies are available and executed
	if !r.CheckAllDependenciesExecuted(inst) {
		GetLogger().Debug(EXECUTION, "Instance R%d.%d dependencies not all executed yet", replicaID, instanceID)
		return false
	}

	// Step 1: Build dependency graph
	graph := r.BuildDependencyGraph(replicaID, instanceID)

	// Check if the target instance is in the graph
	targetKey := makeKey(replicaID, instanceID)
	if _, exists := graph.Nodes[targetKey]; !exists {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d not found in dependency graph", replicaID, instanceID)
		return false
	}

	// Step 2: Find strongly connected components
	sccs := graph.StronglyConnectedComponents()

	// Step 3: Sort SCCs topologically (reverse order since we want inverse topological order)
	// In this simplified version, we'll execute in the order we found them

	// Step 4: Execute commands in each SCC
	executed := false
	for _, scc := range sccs {
		// Sort commands in this SCC by sequence number
		var sccInstances []*GraphNode
		for _, nodeKey := range scc {
			if node, exists := graph.Nodes[nodeKey]; exists {
				sccInstances = append(sccInstances, node)
			}
		}

		// Sort by sequence number, then by replica ID for deterministic ordering
		sort.Slice(sccInstances, func(i, j int) bool {
			if sccInstances[i].Instance.Seq != sccInstances[j].Instance.Seq {
				return sccInstances[i].Instance.Seq < sccInstances[j].Instance.Seq
			}
			return sccInstances[i].ReplicaID < sccInstances[j].ReplicaID
		})

		// Execute all commands in this SCC
		for _, node := range sccInstances {
			wasExecuted := r.executeCommand(node.ReplicaID, node.InstanceID, node.Instance)
			if node.ReplicaID == replicaID && node.InstanceID == instanceID {
				executed = wasExecuted
			}
		}
	}

	if !executed {
		GetLogger().Warn(EXECUTION, "Target instance R%d.%d was not executed in any SCC", replicaID, instanceID)
	}

	return executed
}

// executeCommand executes a single command and marks it as executed
func (r *Replica) executeCommand(replicaID, instanceID int, inst *EPaxosInstance) bool {
	if inst.Executed {
		return true
	}

	// Check if this is a no-op command
	if inst.Command.Key == "__noop__" {
		inst.Executed = true
		inst.Status = StatusExecuted
		LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, "noop")
		return true
	}

	// Apply the command to the local KV store
	oldStatus := inst.Status
	result, err := r.KVStore.ApplyCommand(inst.Command)
	if err != nil {
		LogExecutionFailure(ReplicaID(replicaID), instanceID, inst.Command, err)
		return false
	}

	inst.Executed = true
	inst.Status = StatusExecuted

	LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, result)
	LogInstanceStateChange(ReplicaID(replicaID), instanceID, oldStatus, inst.Status, inst)
	return true
}

// CheckAllDependenciesExecuted verifies that all dependencies of an instance have been executed
func (r *Replica) CheckAllDependenciesExecuted(inst *EPaxosInstance) bool {
	for _, dep := range inst.Deps {
		depInstanceMap, exists := r.Instances[dep.ReplicaID]
		if !exists {
			GetLogger().Debug(EXECUTION, "Dependency R%d.%d: replica map not found", dep.ReplicaID, dep.InstanceID)
			// Try to recover the missing dependency
			go r.RecoverInstance(dep.ReplicaID, dep.InstanceID)
			return false
		}
		depInst, exists := depInstanceMap[dep.InstanceID]
		if !exists {
			GetLogger().Debug(EXECUTION, "Dependency R%d.%d: instance not found", dep.ReplicaID, dep.InstanceID)
			// Try to recover the missing dependency
			go r.RecoverInstance(dep.ReplicaID, dep.InstanceID)
			return false
		}
		if !depInst.Executed {
			GetLogger().Debug(EXECUTION, "Dependency R%d.%d: not yet executed", dep.ReplicaID, dep.InstanceID)
			return false
		}
	}
	return true
}

// RecoverInstance attempts to recover a potentially failed instance
func (r *Replica) RecoverInstance(replicaID, instanceID int) {
	GetLogger().Info(CONSENSUS, "Attempting to recover instance R%d.%d", replicaID, instanceID)

	// Add a small delay to avoid thundering herd
	time.Sleep(time.Duration(r.ID) * 100 * time.Millisecond)

	// Check if the instance already exists after the delay
	r.InstanceLock.RLock()
	if instanceMap, exists := r.Instances[replicaID]; exists {
		if inst, exists := instanceMap[instanceID]; exists && inst.Committed {
			r.InstanceLock.RUnlock()
			GetLogger().Debug(CONSENSUS, "Instance R%d.%d already recovered", replicaID, instanceID)
			return
		}
	}
	r.InstanceLock.RUnlock()

	err := r.ExplicitPrepare(replicaID, instanceID)
	if err != nil {
		GetLogger().Error(CONSENSUS, "Failed to recover instance R%d.%d: %v", replicaID, instanceID, err)
	} else {
		GetLogger().Info(CONSENSUS, "Successfully initiated recovery for instance R%d.%d", replicaID, instanceID)
	}
}

// GetInstance safely retrieves an instance with proper error handling
func (r *Replica) GetInstance(replicaID, instanceID int) (*EPaxosInstance, bool) {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

	instanceMap, exists := r.Instances[replicaID]
	if !exists {
		return nil, false
	}

	inst, exists := instanceMap[instanceID]
	if !exists {
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
