package main

// EPaxosInstance represents a single consensus instance on a replica
type EPaxosInstance struct {
	Command   Command   // The client command being agreed on
	CommandID CommandID // Unique ID for the command
	Seq       int       // Sequence number (logical position)
	Deps      []int     // Dependencies: instance IDs from other replicas
	Status    InstanceStatus
	Ballot    int       // Ballot number (for conflict resolution)
	Committed bool      // True if the command is committed
	Executed  bool      // True if the command has been applied
	Timestamp Timestamp // Logical time for ordering
	Leader    bool      // Whether this replica is the leader for the instance
}
