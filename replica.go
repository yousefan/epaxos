// Updated replica.go
package main

import (
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
	for _, depID := range inst.Deps {
		depInst, ok := r.Instances[replicaID][depID]
		if !ok || !depInst.Executed {
			GetLogger().Debug(EXECUTION, "Replica %d: Cannot execute instance %d, dependency %d not executed",
				r.ID, instanceID, depID)
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
