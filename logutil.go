// logutil.go
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// formatDeps formats the dependencies array for better readability
func formatDeps(deps []int) string {
	if len(deps) == 0 {
		return "[]"
	}
	return fmt.Sprintf("%v", deps)
}

// formatDependencies formats the new Dependency slice for better readability
func formatDependencies(deps []Dependency) string {
	if len(deps) == 0 {
		return "[]"
	}
	var strs []string
	for _, dep := range deps {
		strs = append(strs, fmt.Sprintf("R%d.%d", dep.ReplicaID, dep.InstanceID))
	}
	return fmt.Sprintf("[%s]", strings.Join(strs, ", "))
}

// formatCommand formats a Command for logging
func formatCommand(cmd Command) string {
	switch cmd.Type {
	case CmdGet:
		return fmt.Sprintf("GET(%s)", cmd.Key)
	case CmdPut:
		return fmt.Sprintf("PUT(%s, %s)", cmd.Key, cmd.Value)
	default:
		return "UNKNOWN"
	}
}

// formatCommandID formats a CommandID for logging
func formatCommandID(cmdID CommandID) string {
	return fmt.Sprintf("%s:%d", cmdID.ClientID, cmdID.SeqNum)
}

// formatStatus formats an InstanceStatus for logging
func formatStatus(status InstanceStatus) string {
	switch status {
	case StatusNone:
		return "NONE"
	case StatusPreAccepted:
		return "PRE-ACCEPTED"
	case StatusAccepted:
		return "ACCEPTED"
	case StatusCommitted:
		return "COMMITTED"
	case StatusExecuted:
		return "EXECUTED"
	default:
		return "UNKNOWN"
	}
}

// formatBallot formats a Ballot for logging
func formatBallot(ballot Ballot) string {
	return fmt.Sprintf("%d.%d.%d", ballot.Epoch, ballot.Sequence, ballot.ReplicaID)
}

// formatInstance formats an EPaxosInstance for logging
func formatInstance(inst *EPaxosInstance) string {
	if inst == nil {
		return "nil"
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Command: %s, ", formatCommand(inst.Command)))
	builder.WriteString(fmt.Sprintf("ID: %s, ", formatCommandID(inst.CommandID)))
	builder.WriteString(fmt.Sprintf("Seq: %d, ", inst.Seq))
	builder.WriteString(fmt.Sprintf("Deps: %s, ", formatDependencies(inst.Deps)))
	builder.WriteString(fmt.Sprintf("Status: %s, ", formatStatus(inst.Status)))
	builder.WriteString(fmt.Sprintf("Ballot: %s, ", formatBallot(inst.Ballot)))
	builder.WriteString(fmt.Sprintf("Committed: %t, ", inst.Committed))
	builder.WriteString(fmt.Sprintf("Executed: %t", inst.Executed))

	return builder.String()
}

func LogFastPath() {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(COMMIT, "Fast path: committing directly")
}

func LogSlowPath() {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(ACCEPT, "Slow path: sending Accept")
}

func LogAcceptQuorum() {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(ACCEPT, "Accept quorum achieved: committing")
}

// LogAcceptQuorumFailure logs that an Accept quorum was not achieved and the process will retry or abort
func LogAcceptQuorumFailure() {
	if GetLogger() == nil {
		return
	}

	GetLogger().Warn(ACCEPT, "Accept quorum not achieved. Will retry or abort.")
}

// LogInstanceStateChange logs a change in an EPaxos instance's state
func LogInstanceStateChange(replicaID ReplicaID, instanceID int, oldState, newState InstanceStatus, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(CONSENSUS, "Instance R%d.%d state change: %s -> %s | %s",
		replicaID, instanceID, formatStatus(oldState), formatStatus(newState), formatInstance(instance))
}

// LogPreAcceptPhase logs the beginning of a PreAccept phase
func LogPreAcceptPhase(replicaID ReplicaID, instanceID int, command Command, cmdID CommandID) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(PREACCEPT, "Starting PreAccept phase for instance R%d.%d | Command: %s | ID: %s",
		replicaID, instanceID, formatCommand(command), formatCommandID(cmdID))
}

// LogPreAcceptResponse logs a response to a PreAccept request
func LogPreAcceptResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID,
	initialSeq, newSeq int, initialDeps, newDeps []int, success bool) {
	if GetLogger() == nil {
		return
	}

	if success {
		if initialSeq != newSeq || !equalIntSlice(initialDeps, newDeps) {
			GetLogger().Info(PREACCEPT, "PreAccept for instance R%d.%d from R%d: CONFLICT | Seq: %d -> %d | Deps: %s -> %s",
				replicaID, instanceID, fromReplica, initialSeq, newSeq, formatDeps(initialDeps), formatDeps(newDeps))
		} else {
			GetLogger().Debug(PREACCEPT, "PreAccept for instance R%d.%d from R%d: OK | Seq: %d | Deps: %s",
				replicaID, instanceID, fromReplica, newSeq, formatDeps(newDeps))
		}
	} else {
		GetLogger().Warn(PREACCEPT, "PreAccept for instance R%d.%d from R%d: FAILED",
			replicaID, instanceID, fromReplica)
	}
}

// LogAcceptPhase logs the beginning of an Accept phase
func LogAcceptPhase(replicaID ReplicaID, instanceID int, seq int, deps []int, ballot int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(ACCEPT, "Starting Accept phase for instance R%d.%d | Seq: %d | Deps: %s | Ballot: %d",
		replicaID, instanceID, seq, formatDeps(deps), ballot)
}

// LogAcceptResponse logs a response to an Accept request
func LogAcceptResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID, ballot int, success bool) {
	if GetLogger() == nil {
		return
	}

	if success {
		GetLogger().Debug(ACCEPT, "Accept for instance R%d.%d from R%d: OK | Ballot: %d",
			replicaID, instanceID, fromReplica, ballot)
	} else {
		GetLogger().Warn(ACCEPT, "Accept for instance R%d.%d from R%d: FAILED | Ballot: %d",
			replicaID, instanceID, fromReplica, ballot)
	}
}

// LogCommitPhase logs the beginning of a Commit phase
func LogCommitPhase(replicaID ReplicaID, instanceID int, seq int, deps []int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(COMMIT, "Starting Commit phase for instance R%d.%d | Seq: %d | Deps: %s",
		replicaID, instanceID, seq, formatDeps(deps))
}

// LogCommitResponse logs a response to a Commit request
func LogCommitResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID, success bool) {
	if GetLogger() == nil {
		return
	}

	if success {
		GetLogger().Debug(COMMIT, "Commit for instance R%d.%d from R%d: OK",
			replicaID, instanceID, fromReplica)
	} else {
		GetLogger().Warn(COMMIT, "Commit for instance R%d.%d from R%d: FAILED",
			replicaID, instanceID, fromReplica)
	}
}

// LogExecutionAttempt logs an attempt to execute an instance
func LogExecutionAttempt(replicaID ReplicaID, instanceID int, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Debug(EXECUTION, "Attempting execution for instance R%d.%d | %s",
		replicaID, instanceID, formatInstance(instance))
}

// LogExecutionSuccess logs successful execution of an instance
func LogExecutionSuccess(replicaID ReplicaID, instanceID int, command Command, result string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(EXECUTION, "Successfully executed instance R%d.%d | Command: %s | Result: %s",
		replicaID, instanceID, formatCommand(command), result)
}

// LogExecutionFailure logs failed execution of an instance
func LogExecutionFailure(replicaID ReplicaID, instanceID int, command Command, err error) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Error(EXECUTION, "Failed to execute instance R%d.%d | Command: %s | Error: %v",
		replicaID, instanceID, formatCommand(command), err)
}

// LogConflictDetection logs conflict detection between commands
func LogConflictDetection(replicaID ReplicaID, instanceID int, otherReplicaID int, otherInstanceID int,
	command Command, otherCommand Command) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Debug(DEPENDENCY, "Conflict detected for instance R%d.%d with R%d.%d | Command: %s conflicts with %s",
		replicaID, instanceID, otherReplicaID, otherInstanceID,
		formatCommand(command), formatCommand(otherCommand))
}

// LogDependencyAdded logs when a dependency is added
func LogDependencyAdded(replicaID ReplicaID, instanceID int, depInstanceID int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Debug(DEPENDENCY, "Added dependency for instance R%d.%d -> %d",
		replicaID, instanceID, depInstanceID)
}

// LogReplicaStart logs when a replica starts
func LogReplicaStart(replicaID ReplicaID, address string, peers []string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(REPLICA, "Replica R%d started at %s with peers: %v",
		replicaID, address, peers)
}

// LogClientRequest logs a client request
func LogClientRequest(replicaID ReplicaID, command Command, cmdID CommandID) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Info(CLIENT, "Received client request on R%d | Command: %s | ID: %s",
		replicaID, formatCommand(command), formatCommandID(cmdID))
}

// LogRPCCall logs an outgoing RPC call
func LogRPCCall(replicaID ReplicaID, target string, method string, args interface{}) {
	if GetLogger() == nil {
		return
	}

	argsJSON, _ := json.Marshal(args)
	GetLogger().Debug(RPC, "R%d sending RPC to %s | Method: %s | Args: %s",
		replicaID, target, method, string(argsJSON))
}

// LogRPCReceive logs an incoming RPC call
func LogRPCReceive(replicaID ReplicaID, method string, args interface{}) {
	if GetLogger() == nil {
		return
	}

	argsJSON, _ := json.Marshal(args)
	GetLogger().Debug(RPC, "R%d received RPC | Method: %s | Args: %s",
		replicaID, method, string(argsJSON))
}

// LogKVStoreOperation logs a key-value store operation
func LogKVStoreOperation(replicaID ReplicaID, operation string, key string, value string, success bool, err error) {
	if GetLogger() == nil {
		return
	}

	if success {
		GetLogger().Debug(STORAGE, "R%d KV operation: %s | Key: %s | Value: %s | Success: true",
			replicaID, operation, key, value)
	} else {
		GetLogger().Warn(STORAGE, "R%d KV operation: %s | Key: %s | Value: %s | Success: false | Error: %v",
			replicaID, operation, key, value, err)
	}
}

// Helper function for backward compatibility
func equalIntSlice(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
