package main

import (
	"fmt"
	"sort"
	"sync"
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
			return
		}
		instance, exists := instanceMap[iid]
		if !exists || !instance.Committed {
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
	return graph
}

// TryExecute attempts to execute a committed command following the EPaxos execution algorithm
func (r *Replica) TryExecute(replicaID int, instanceID int) bool {
	r.InstanceLock.Lock()
	defer r.InstanceLock.Unlock()

	instanceMap, ok := r.Instances[replicaID]
	if !ok {
		return false
	}
	inst, ok := instanceMap[instanceID]
	if !ok || !inst.Committed || inst.Executed {
		return false
	}

	LogExecutionAttempt(ReplicaID(replicaID), instanceID, inst)

	// Step 1: Build dependency graph
	graph := r.BuildDependencyGraph(replicaID, instanceID)

	// Step 2: Find strongly connected components
	sccs := graph.StronglyConnectedComponents()

	// Step 3: Sort SCCs topologically (reverse order since we want inverse topological order)
	// In this simplified version, we'll execute in the order we found them

	// Step 4: Execute commands in each SCC
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
			r.executeCommand(node.ReplicaID, node.InstanceID, node.Instance)
		}
	}

	return true
}

// executeCommand executes a single command and marks it as executed
func (r *Replica) executeCommand(replicaID, instanceID int, inst *EPaxosInstance) {
	if inst.Executed {
		return
	}

	// Check if this is a no-op command
	if inst.Command.Key == "__noop__" {
		inst.Executed = true
		inst.Status = StatusExecuted
		LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, "noop")
		return
	}

	// Apply the command to the local KV store
	oldStatus := inst.Status
	result, err := r.KVStore.ApplyCommand(inst.Command)
	if err != nil {
		LogExecutionFailure(ReplicaID(replicaID), instanceID, inst.Command, err)
		return
	}

	inst.Executed = true
	inst.Status = StatusExecuted

	LogExecutionSuccess(ReplicaID(replicaID), instanceID, inst.Command, result)
	LogInstanceStateChange(ReplicaID(replicaID), instanceID, oldStatus, inst.Status, inst)
}

// CheckAllDependenciesExecuted verifies that all dependencies of an instance have been executed
func (r *Replica) CheckAllDependenciesExecuted(inst *EPaxosInstance) bool {
	for _, dep := range inst.Deps {
		depInstanceMap, exists := r.Instances[dep.ReplicaID]
		if !exists {
			return false
		}
		depInst, exists := depInstanceMap[dep.InstanceID]
		if !exists || !depInst.Executed {
			return false
		}
	}
	return true
}

// RecoverInstance attempts to recover a potentially failed instance
func (r *Replica) RecoverInstance(replicaID, instanceID int) {
	GetLogger().Info(CONSENSUS, "Attempting to recover instance %d.%d", replicaID, instanceID)
	err := r.ExplicitPrepare(replicaID, instanceID)
	if err != nil {
		GetLogger().Error(CONSENSUS, "Failed to recover instance %d.%d: %v", replicaID, instanceID, err)
	}
}
