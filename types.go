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
