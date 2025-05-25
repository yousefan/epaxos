package main

import (
	"log"
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

	// Check all dependencies are executed
	for _, depID := range inst.Deps {
		depInst, ok := r.Instances[replicaID][depID]
		if !ok || !depInst.Executed {
			return false // cannot execute yet
		}
	}

	// Apply the command to the local KV store
	_, err := r.KVStore.ApplyCommand(inst.Command)
	if err != nil {
		log.Printf("Failed to execute instance %d: %v", instanceID, err)
		return false
	}

	inst.Executed = true
	inst.Status = StatusExecuted
	log.Printf("Replica %d: Executed instance %d (%s)", r.ID, instanceID, inst.Command.Key)
	return true
}
