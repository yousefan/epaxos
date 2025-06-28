package main

// EPaxosInstance represents a single consensus instance on a replica
type EPaxosInstance struct {
	Command   Command      // The client command being agreed on
	CommandID CommandID    // Unique ID for the command
	Seq       int          // Sequence number (logical position)
	Deps      []Dependency // Dependencies: (replica, instance) pairs
	Status    InstanceStatus
	Ballot    Ballot    // Ballot number (for conflict resolution)
	Committed bool      // True if the command is committed
	Executed  bool      // True if the command has been applied
	Timestamp Timestamp // Logical time for ordering
	Leader    bool      // Whether this replica is the leader for the instance
}

// HasDependency checks if this instance depends on another instance
func (inst *EPaxosInstance) HasDependency(replicaID, instanceID int) bool {
	for _, dep := range inst.Deps {
		if dep.ReplicaID == replicaID && dep.InstanceID == instanceID {
			return true
		}
	}
	return false
}

// AddDependency adds a dependency if not already present
func (inst *EPaxosInstance) AddDependency(replicaID, instanceID int) {
	if !inst.HasDependency(replicaID, instanceID) {
		inst.Deps = append(inst.Deps, Dependency{
			ReplicaID:  replicaID,
			InstanceID: instanceID,
		})
	}
}
