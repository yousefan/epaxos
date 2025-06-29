package main

// EPaxosInstance represents a single consensus instance on a replica
type EPaxosInstance struct {
	Command       Command                  // The client command being agreed on
	CommandID     CommandID                // Unique ID for the command
	Seq           int                      // Sequence number (logical position)
	Deps          []CrossReplicaDependency // Dependencies: ordered pairs (replicaID, instanceID)
	Status        InstanceStatus
	Ballot        Ballot      // Ballot number (for conflict resolution and leader election)
	Committed     bool        // True if the command is committed
	Executed      bool        // True if the command has been applied
	Timestamp     Timestamp   // Logical time for ordering
	Leader        ReplicaID   // Leader replica for this instance
	Quorum        int         // Quorum size for this instance
	PreAcceptedBy []ReplicaID // Replicas that have pre-accepted this instance
	AcceptedBy    []ReplicaID // Replicas that have accepted this instance
	CommittedBy   []ReplicaID // Replicas that have committed this instance
}
