// Updated replica.go
package main

import (
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

	// Leader election and ballot management
	CurrentBallot Ballot               // Current ballot number
	BallotLock    sync.RWMutex         // Protects ballot operations
	LeaderBallot  map[ReplicaID]Ballot // Track highest ballot seen from each replica

	// Client request deduplication
	ClientRequests map[CommandID]*ClientRequest // Track client requests
	ClientLock     sync.RWMutex                 // Protects client requests

	// Quorum calculation
	QuorumSize     int // Slow-path quorum size
	FastPathQuorum int // Fast-path quorum size

	// Recovery state
	RecoveryMode bool // Whether replica is in recovery mode
}

// NewReplica creates and initializes a new replica
func NewReplica(id ReplicaID, peers []string) *Replica {
	totalReplicas := len(peers) + 1 // Including self
	f := (totalReplicas - 1) / 2    // Maximum number of failures

	// Fast-path quorum: F + ⌊(F+1)/2⌋
	fastPathQuorum := f + (f+1)/2

	// Slow-path quorum: majority (⌊(F+1)/2⌋ + 1)
	slowPathQuorum := (f+1)/2 + 1

	return &Replica{
		ID:             id,
		Peers:          peers,
		Instances:      make(map[int]map[int]*EPaxosInstance),
		KVStore:        NewKVStore(),
		NextInstance:   0,
		CurrentBallot:  Ballot{Number: 0, ReplicaID: id},
		LeaderBallot:   make(map[ReplicaID]Ballot),
		ClientRequests: make(map[CommandID]*ClientRequest),
		QuorumSize:     slowPathQuorum,
		FastPathQuorum: fastPathQuorum,
		RecoveryMode:   false,
	}
}

// GetNextBallot generates the next ballot number for this replica
func (r *Replica) GetNextBallot() Ballot {
	r.BallotLock.Lock()
	defer r.BallotLock.Unlock()

	r.CurrentBallot.Number++
	return r.CurrentBallot
}

// UpdateBallot updates the current ballot if the new one is higher
func (r *Replica) UpdateBallot(newBallot Ballot) bool {
	r.BallotLock.Lock()
	defer r.BallotLock.Unlock()

	if newBallot.Less(r.CurrentBallot) {
		return false
	}

	r.CurrentBallot = newBallot
	r.LeaderBallot[newBallot.ReplicaID] = newBallot
	return true
}

// IsQuorum checks if the given count constitutes a quorum
func (r *Replica) IsQuorum(count int) bool {
	return count >= r.QuorumSize
}

// TrackClientRequest tracks a client request for deduplication
func (r *Replica) TrackClientRequest(req *ClientRequest) bool {
	r.ClientLock.Lock()
	defer r.ClientLock.Unlock()

	if existing, exists := r.ClientRequests[req.CommandID]; exists {
		// Return existing result if request is recent enough
		if time.Since(existing.Timestamp) < 30*time.Second {
			return false // Duplicate request
		}
	}

	r.ClientRequests[req.CommandID] = req
	return true
}

// GetClientRequest retrieves a tracked client request
func (r *Replica) GetClientRequest(cmdID CommandID) (*ClientRequest, bool) {
	r.ClientLock.RLock()
	defer r.ClientLock.RUnlock()

	req, exists := r.ClientRequests[cmdID]
	return req, exists
}

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

	LogExecutionAttempt(r.ID, instanceID, inst)

	// Check all dependencies are executed
	for _, dep := range inst.Deps {
		depInst, ok := r.Instances[int(dep.ReplicaID)][dep.InstanceID]
		if !ok || !depInst.Executed {
			GetLogger().Debug(EXECUTION, "Replica %d: Cannot execute instance %d, dependency R%d.%d not executed",
				r.ID, instanceID, dep.ReplicaID, dep.InstanceID)
			return false // cannot execute yet
		}
	}

	// Apply the command to the local KV store
	oldStatus := inst.Status
	result, err := r.KVStore.ApplyCommand(inst.Command)
	if err != nil {
		LogExecutionFailure(r.ID, instanceID, inst.Command, err)
		return false
	}

	inst.Executed = true
	inst.Status = StatusExecuted

	LogExecutionSuccess(r.ID, instanceID, inst.Command, result)
	LogInstanceStateChange(r.ID, instanceID, oldStatus, inst.Status, inst)

	return true
}

// StartRecovery initiates recovery mode for this replica
func (r *Replica) StartRecovery() {
	r.RecoveryMode = true
	GetLogger().Info(REPLICA, "Replica %d entering recovery mode", r.ID)

	// TODO: Implement recovery logic
	// 1. Send recovery requests to all peers
	// 2. Collect missing instances
	// 3. Reconstruct dependency graph
	// 4. Execute missing instances in correct order
}

// EndRecovery ends recovery mode
func (r *Replica) EndRecovery() {
	r.RecoveryMode = false
	GetLogger().Info(REPLICA, "Replica %d exiting recovery mode", r.ID)
}

// GetMissingInstances returns instances that this replica is missing
func (r *Replica) GetMissingInstances() map[ReplicaID][]int {
	missing := make(map[ReplicaID][]int)

	// TODO: Implement logic to determine missing instances
	// This would involve comparing with other replicas

	return missing
}

// ExecuteInstanceSafely ensures state machine safety by checking dependencies
func (r *Replica) ExecuteInstanceSafely(replicaID int, instanceID int) bool {
	r.InstanceLock.RLock()
	instanceMap, ok := r.Instances[replicaID]
	if !ok {
		r.InstanceLock.RUnlock()
		return false
	}
	inst, ok := instanceMap[instanceID]
	if !ok || !inst.Committed || inst.Executed {
		r.InstanceLock.RUnlock()
		return false
	}

	// Check if all dependencies are executed
	for _, dep := range inst.Deps {
		depInst, ok := r.Instances[int(dep.ReplicaID)][dep.InstanceID]
		if !ok || !depInst.Executed {
			r.InstanceLock.RUnlock()
			GetLogger().Debug(EXECUTION, "Replica %d: Cannot execute instance %d, dependency R%d.%d not executed",
				r.ID, instanceID, dep.ReplicaID, dep.InstanceID)
			return false
		}
	}
	r.InstanceLock.RUnlock()

	// Now execute with write lock
	return r.TryExecute(replicaID, instanceID)
}

// FindSCCs finds strongly connected components in the dependency graph
func (r *Replica) FindSCCs() [][]CrossReplicaDependency {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

	// Build dependency graph
	graph := make(map[CrossReplicaDependency][]CrossReplicaDependency)
	var nodes []CrossReplicaDependency

	for rid, instanceMap := range r.Instances {
		for iid, inst := range instanceMap {
			if inst.Committed && !inst.Executed {
				node := CrossReplicaDependency{ReplicaID: ReplicaID(rid), InstanceID: iid}
				nodes = append(nodes, node)
				graph[node] = inst.Deps
			}
		}
	}

	// Tarjan's algorithm for finding SCCs
	var sccs [][]CrossReplicaDependency
	index := make(map[CrossReplicaDependency]int)
	lowlink := make(map[CrossReplicaDependency]int)
	onStack := make(map[CrossReplicaDependency]bool)
	var stack []CrossReplicaDependency
	var currentIndex int

	var strongConnect func(node CrossReplicaDependency)
	strongConnect = func(node CrossReplicaDependency) {
		index[node] = currentIndex
		lowlink[node] = currentIndex
		currentIndex++
		stack = append(stack, node)
		onStack[node] = true

		for _, neighbor := range graph[node] {
			if _, exists := index[neighbor]; !exists {
				strongConnect(neighbor)
				if lowlink[neighbor] < lowlink[node] {
					lowlink[node] = lowlink[neighbor]
				}
			} else if onStack[neighbor] {
				if index[neighbor] < lowlink[node] {
					lowlink[node] = index[neighbor]
				}
			}
		}

		if lowlink[node] == index[node] {
			var scc []CrossReplicaDependency
			for {
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[top] = false
				scc = append(scc, top)
				if top == node {
					break
				}
			}
			if len(scc) > 1 {
				sccs = append(sccs, scc)
			}
		}
	}

	for _, node := range nodes {
		if _, exists := index[node]; !exists {
			strongConnect(node)
		}
	}

	return sccs
}

// ExecuteSCCs executes instances in SCC order to prevent divergent execution
func (r *Replica) ExecuteSCCs() {
	sccs := r.FindSCCs()

	for _, scc := range sccs {
		GetLogger().Info(EXECUTION, "Found SCC with %d instances: %v", len(scc), scc)

		// Execute all instances in the SCC with the same sequence number
		// This ensures they are executed in a consistent order across replicas
		for _, dep := range scc {
			r.ExecuteInstanceSafely(int(dep.ReplicaID), dep.InstanceID)
		}
	}
}

// VerifyExecutionOrder ensures that instances are executed in the correct order
func (r *Replica) VerifyExecutionOrder() bool {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

	for rid, instanceMap := range r.Instances {
		executedInstances := make([]int, 0)
		for iid, inst := range instanceMap {
			if inst.Executed {
				executedInstances = append(executedInstances, iid)
			}
		}

		// Sort executed instances by sequence number
		sort.Slice(executedInstances, func(i, j int) bool {
			instI := instanceMap[executedInstances[i]]
			instJ := instanceMap[executedInstances[j]]
			return instI.Seq < instJ.Seq
		})

		// Verify that dependencies are satisfied
		for _, iid := range executedInstances {
			inst := instanceMap[iid]
			for _, dep := range inst.Deps {
				depInst, exists := r.Instances[int(dep.ReplicaID)][dep.InstanceID]
				if !exists || !depInst.Executed {
					GetLogger().Error(EXECUTION, "Execution order violation: replica %d instance %d executed before dependency R%d.%d",
						rid, iid, dep.ReplicaID, dep.InstanceID)
					return false
				}
			}
		}
	}

	return true
}
