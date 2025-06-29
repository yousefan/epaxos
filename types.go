package main

import "time"

// CommandID uniquely identifies a command
type CommandID struct {
	ClientID string // ID of the client issuing the command
	SeqNum   int    // Monotonically increasing number per client
}

// ReplicaID is an alias for better readability
type ReplicaID int

// Ballot represents a ballot number for leader election
type Ballot struct {
	Number    int       // Ballot number
	ReplicaID ReplicaID // Replica that proposed this ballot
}

// Less returns true if this ballot is less than the other
func (b Ballot) Less(other Ballot) bool {
	if b.Number != other.Number {
		return b.Number < other.Number
	}
	return b.ReplicaID < other.ReplicaID
}

// Equal returns true if this ballot equals the other
func (b Ballot) Equal(other Ballot) bool {
	return b.Number == other.Number && b.ReplicaID == other.ReplicaID
}

// InstanceStatus indicates the state of an EPaxos instance
type InstanceStatus int

const (
	// Instance statuses
	StatusNone     InstanceStatus = iota
	StatusPrepared                // New: Instance has been prepared
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

// ClientRequest represents a client request with deduplication
type ClientRequest struct {
	CommandID CommandID
	Command   Command
	Timestamp time.Time
}

// === Prepare Phase Structures ===

// PrepareArgs represents a Prepare request
type PrepareArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Ballot     Ballot
}

// PrepareReply represents a Prepare response
type PrepareReply struct {
	OK      bool
	Ballot  Ballot
	Command Command
	Seq     int
	Deps    []CrossReplicaDependency
	Status  InstanceStatus
}

// NACK represents a negative acknowledgment
type NACK struct {
	Ballot Ballot
	Reason string
}

// === Durable Log Structures ===

// LogEntry represents a durable log entry
type LogEntry struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []CrossReplicaDependency
	Status     InstanceStatus
	Ballot     Ballot
	Timestamp  time.Time
	Committed  bool
	Executed   bool
}

// LogPosition represents a position in the durable log
type LogPosition struct {
	ReplicaID  ReplicaID
	InstanceID int
	Offset     int64 // Byte offset in log file
}
