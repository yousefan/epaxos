package main

import "time"

// CommandID uniquely identifies a command
type CommandID struct {
	ClientID string // ID of the client issuing the command
	SeqNum   int    // Monotonically increasing number per client
}

// ReplicaID is an alias for better readability
type ReplicaID int

// InstanceStatus indicates the state of an EPaxos instance
type InstanceStatus int

const (
	// Instance statuses
	StatusNone InstanceStatus = iota
	StatusPreAccepted
	StatusAccepted
	StatusCommitted
	StatusExecuted
)

// CommandType defines if the command is a read or write
type CommandType int

const (
	CmdGet CommandType = iota
	CmdPut
)

// Command represents a client operation
type Command struct {
	Type  CommandType // CmdGet or CmdPut
	Key   string
	Value string // Used only for Put
}

// Timestamp helps with EPaxos ordering (logical time)
type Timestamp struct {
	Time  time.Time
	Count int // For tie-breaking in the same time
}

// Dependency represents a dependency on another instance
type Dependency struct {
	ReplicaID  int
	InstanceID int
}

// Ballot represents a ballot number with epoch, sequence, and replica ID
type Ballot struct {
	Epoch     int
	Sequence  int
	ReplicaID int
}

// CompareBallots returns -1 if b1 < b2, 0 if equal, 1 if b1 > b2
func CompareBallots(b1, b2 Ballot) int {
	if b1.Epoch != b2.Epoch {
		if b1.Epoch < b2.Epoch {
			return -1
		}
		return 1
	}
	if b1.Sequence != b2.Sequence {
		if b1.Sequence < b2.Sequence {
			return -1
		}
		return 1
	}
	if b1.ReplicaID != b2.ReplicaID {
		if b1.ReplicaID < b2.ReplicaID {
			return -1
		}
		return 1
	}
	return 0
}
