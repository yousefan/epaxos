// logutil.go - Enhanced structured logging utilities
package main

import (
	"fmt"
	"time"
)

// === EPaxos Phase Logging ===

func LogPreAcceptPhase(replicaID ReplicaID, instanceID int, command Command, cmdID CommandID) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, PREACCEPT, "Starting PreAccept phase").
		WithInstance(int(replicaID), instanceID).
		WithCommand(command, cmdID).
		WithPhase("preaccept").
		WithTags("phase_start", "consensus").
		Send()
}

func LogPreAcceptResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID,
	initialSeq, newSeq int, initialDeps, newDeps []Dependency, success bool, attributesUnchanged bool) {
	if GetLogger() == nil {
		return
	}

	msg := "PreAccept response received"
	if !success {
		msg = "PreAccept response failed"
	}

	logger := GetLogger().Log(INFO, PREACCEPT, msg).
		WithInstance(int(replicaID), instanceID).
		WithSequenceChange(initialSeq, newSeq).
		WithDependencyChange(initialDeps, newDeps).
		WithContext("from_replica", int(fromReplica)).
		WithContext("success", success).
		WithAttributesUnchanged(attributesUnchanged).
		WithPhase("preaccept").
		WithTags("response", "consensus")

	if initialSeq != newSeq || !equalDependencySlice(initialDeps, newDeps) {
		logger.WithTags("conflict_detected")
	}

	logger.Send()
}

func LogAcceptPhase(replicaID ReplicaID, instanceID int, seq int, deps []Dependency, ballot Ballot) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, ACCEPT, "Starting Accept phase").
		WithInstance(int(replicaID), instanceID).
		WithSequence(seq).
		WithDependencies(deps).
		WithBallot(ballot).
		WithPhase("accept").
		WithTags("phase_start", "consensus").
		Send()
}

func LogAcceptResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID, ballot Ballot, success bool) {
	if GetLogger() == nil {
		return
	}

	msg := "Accept response received"
	if !success {
		msg = "Accept response failed"
	}

	GetLogger().Log(INFO, ACCEPT, msg).
		WithInstance(int(replicaID), instanceID).
		WithBallot(ballot).
		WithContext("from_replica", int(fromReplica)).
		WithContext("success", success).
		WithPhase("accept").
		WithTags("response", "consensus").
		Send()
}

func LogCommitPhase(replicaID ReplicaID, instanceID int, seq int, deps []Dependency) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, COMMIT, "Starting Commit phase").
		WithInstance(int(replicaID), instanceID).
		WithSequence(seq).
		WithDependencies(deps).
		WithPhase("commit").
		WithTags("phase_start", "consensus").
		Send()
}

func LogCommitResponse(replicaID ReplicaID, instanceID int, fromReplica ReplicaID, success bool) {
	if GetLogger() == nil {
		return
	}

	msg := "Commit response received"
	if !success {
		msg = "Commit response failed"
	}

	GetLogger().Log(INFO, COMMIT, msg).
		WithInstance(int(replicaID), instanceID).
		WithContext("from_replica", int(fromReplica)).
		WithContext("success", success).
		WithPhase("commit").
		WithTags("response", "consensus").
		Send()
}

// === Consensus Decision Logging ===

func LogFastPath(replicaID ReplicaID, instanceID int, quorumSize, unchanged int, command Command, cmdID CommandID) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, CONSENSUS, "Fast path consensus achieved").
		WithInstance(int(replicaID), instanceID).
		WithQuorum(quorumSize, quorumSize, unchanged).
		WithFastPath(true).
		WithCommand(command, cmdID).
		WithTags("fast_path", "consensus", "optimization").
		Send()
}

func LogSlowPath(replicaID ReplicaID, instanceID int, reason string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, CONSENSUS, "Falling back to slow path").
		WithInstance(int(replicaID), instanceID).
		WithFastPath(false).
		WithContext("reason", reason).
		WithTags("slow_path", "consensus", "fallback").
		Send()
}

func LogAcceptQuorum(replicaID ReplicaID, instanceID int, received, required int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, CONSENSUS, "Accept quorum achieved").
		WithInstance(int(replicaID), instanceID).
		WithQuorum(required, received, 0).
		WithPhase("accept").
		WithTags("quorum", "consensus", "success").
		Send()
}

func LogAcceptQuorumFailure(replicaID ReplicaID, instanceID int, received, required int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(WARN, CONSENSUS, "Accept quorum failed").
		WithInstance(int(replicaID), instanceID).
		WithQuorum(required, received, 0).
		WithPhase("accept").
		WithTags("quorum", "consensus", "failure").
		Send()
}

// === Instance State Logging ===

func LogInstanceStateChange(replicaID ReplicaID, instanceID int, oldState, newState InstanceStatus, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(INFO, CONSENSUS, "Instance state changed").
		WithInstance(int(replicaID), instanceID).
		WithStateChange(oldState, newState)

	if instance != nil {
		if instance.Command != (Command{}) && instance.CommandID != (CommandID{}) {
			logger.WithCommand(instance.Command, instance.CommandID)
		}
		logger.WithSequence(instance.Seq).
			WithDependencies(instance.Deps).
			WithBallot(instance.Ballot).
			WithContext("committed", instance.Committed).
			WithContext("executed", instance.Executed)
	}

	logger.WithTags("state_change", "consensus").Send()
}

// === Execution Logging ===

func LogExecutionAttempt(replicaID ReplicaID, instanceID int, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(DEBUG, EXECUTION, "Attempting instance execution").
		WithInstance(int(replicaID), instanceID)

	if instance != nil {
		if instance.Command != (Command{}) && instance.CommandID != (CommandID{}) {
			logger.WithCommand(instance.Command, instance.CommandID)
		}
		logger.WithSequence(instance.Seq).
			WithDependencies(instance.Deps).
			WithContext("committed", instance.Committed).
			WithContext("executed", instance.Executed)
	}

	logger.WithTags("execution", "attempt").Send()
}

func LogExecutionSuccess(replicaID ReplicaID, instanceID int, command Command, result string, duration time.Duration) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, EXECUTION, "Instance executed successfully").
		WithInstance(int(replicaID), instanceID).
		WithCommand(command, CommandID{}).
		WithContext("result", result).
		WithDuration(duration).
		WithTags("execution", "success").
		Send()
}

func LogExecutionFailure(replicaID ReplicaID, instanceID int, command Command, err error) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(ERROR, EXECUTION, "Instance execution failed").
		WithInstance(int(replicaID), instanceID).
		WithCommand(command, CommandID{}).
		WithError(err, "execution_error").
		WithTags("execution", "failure").
		Send()
}

func LogExecutionOrder(replicaID ReplicaID, instanceID int, order int, sccSize int, graphSize int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, EXECUTION, "Instance execution order determined").
		WithInstance(int(replicaID), instanceID).
		WithExecution(order, sccSize, graphSize).
		WithTags("execution", "ordering").
		Send()
}

// === Dependency and Conflict Logging ===

func LogConflictDetection(replicaID ReplicaID, instanceID int, otherReplicaID int, otherInstanceID int,
	command Command, otherCommand Command) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, DEPENDENCY, "Command conflict detected").
		WithInstance(int(replicaID), instanceID).
		WithCommand(command, CommandID{}).
		WithContext("conflicting_instance", fmt.Sprintf("R%d.%d", otherReplicaID, otherInstanceID)).
		WithContext("conflicting_command", formatCommand(otherCommand)).
		WithTags("conflict", "dependency").
		Send()
}

func LogDependencyAdded(replicaID ReplicaID, instanceID int, depReplicaID int, depInstanceID int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, DEPENDENCY, "Dependency added").
		WithInstance(int(replicaID), instanceID).
		WithContext("dependency", fmt.Sprintf("R%d.%d", depReplicaID, depInstanceID)).
		WithTags("dependency", "added").
		Send()
}

func LogDependencyGraphBuilt(replicaID ReplicaID, instanceID int, nodeCount int, edgeCount int, sccCount int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, EXECUTION, "Dependency graph constructed").
		WithInstance(int(replicaID), instanceID).
		WithContext("node_count", nodeCount).
		WithContext("edge_count", edgeCount).
		WithContext("scc_count", sccCount).
		WithTags("dependency", "graph", "execution").
		Send()
}

func LogMissingDependencies(replicaID ReplicaID, instanceID int, missingDeps []string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(WARN, EXECUTION, "Missing dependencies detected").
		WithInstance(int(replicaID), instanceID).
		WithContext("missing_dependencies", missingDeps).
		WithTags("dependency", "missing", "recovery_needed").
		Send()
}

// === Network and RPC Logging ===

func LogRPCCall(replicaID ReplicaID, target string, method string, args interface{}, startTime time.Time) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, RPC, "Sending RPC request").
		WithRPC(target, method, 0, true).
		WithTimeRange(startTime, time.Time{}).
		WithContext("args", args).
		WithTags("rpc", "outgoing").
		Send()
}

func LogRPCReceive(replicaID ReplicaID, method string, args interface{}) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(DEBUG, RPC, "Received RPC request").
		WithRPC("", method, 0, true).
		WithContext("args", args).
		WithTags("rpc", "incoming").
		Send()
}

func LogRPCComplete(replicaID ReplicaID, target string, method string, duration time.Duration, success bool, err error) {
	if GetLogger() == nil {
		return
	}

	msg := "RPC completed successfully"
	level := DEBUG
	if !success {
		msg = "RPC failed"
		level = WARN
	}

	logger := GetLogger().Log(level, RPC, msg).
		WithRPC(target, method, duration, success).
		WithDuration(duration)

	if err != nil {
		logger.WithError(err, "rpc_error")
	}

	logger.WithTags("rpc", "completed").Send()
}

func LogNetworkPartition(replicaID ReplicaID, unreachablePeers []string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(ERROR, NETWORK, "Network partition detected").
		WithContext("unreachable_peers", unreachablePeers).
		WithContext("peer_count", len(unreachablePeers)).
		WithTags("network", "partition", "failure").
		Send()
}

// === Recovery Logging ===

func LogRecoveryStart(replicaID ReplicaID, targetReplicaID int, instanceID int, reason string, attempt int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, RECOVERY, "Starting instance recovery").
		WithInstance(targetReplicaID, instanceID).
		WithRecovery(reason, attempt).
		WithTags("recovery", "start").
		Send()
}

func LogRecoveryComplete(replicaID ReplicaID, targetReplicaID int, instanceID int, success bool, duration time.Duration) {
	if GetLogger() == nil {
		return
	}

	msg := "Instance recovery completed successfully"
	level := INFO
	if !success {
		msg = "Instance recovery failed"
		level = WARN
	}

	GetLogger().Log(level, RECOVERY, msg).
		WithInstance(targetReplicaID, instanceID).
		WithDuration(duration).
		WithContext("success", success).
		WithTags("recovery", "completed").
		Send()
}

func LogPreparePhase(replicaID ReplicaID, targetReplicaID int, instanceID int, ballot Ballot) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, RECOVERY, "Starting Prepare phase for recovery").
		WithInstance(targetReplicaID, instanceID).
		WithBallot(ballot).
		WithPhase("prepare").
		WithTags("recovery", "prepare").
		Send()
}

func LogPrepareResponse(replicaID ReplicaID, targetReplicaID int, instanceID int, fromReplica int, success bool, committed bool, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	msg := "Prepare response received"
	if !success {
		msg = "Prepare response failed"
	}

	logger := GetLogger().Log(DEBUG, RECOVERY, msg).
		WithInstance(targetReplicaID, instanceID).
		WithContext("from_replica", fromReplica).
		WithContext("success", success).
		WithContext("committed", committed).
		WithPhase("prepare")

	if instance != nil {
		logger.WithCommand(instance.Command, instance.CommandID).
			WithSequence(instance.Seq).
			WithDependencies(instance.Deps).
			WithBallot(instance.Ballot)
	}

	logger.WithTags("recovery", "prepare", "response").Send()
}

// === Client and Replica Lifecycle Logging ===

func LogReplicaStart(replicaID ReplicaID, address string, peers []string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, REPLICA, "Replica started").
		WithContext("address", address).
		WithContext("peers", peers).
		WithContext("peer_count", len(peers)).
		WithTags("replica", "startup", "lifecycle").
		Send()
}

func LogReplicaShutdown(replicaID ReplicaID, reason string) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, REPLICA, "Replica shutting down").
		WithContext("reason", reason).
		WithTags("replica", "shutdown", "lifecycle").
		Send()
}

func LogClientRequest(replicaID ReplicaID, command Command, cmdID CommandID, commandCount int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, CLIENT, "Client request received").
		WithCommand(command, cmdID).
		WithClient(cmdID.ClientID).
		WithContext("command_count", commandCount).
		WithTags("client", "request").
		Send()
}

func LogClientResponse(replicaID ReplicaID, command Command, cmdID CommandID, success bool, result string, duration time.Duration, err error) {
	if GetLogger() == nil {
		return
	}

	msg := "Client request completed"
	level := INFO
	if !success {
		msg = "Client request failed"
		level = WARN
	}

	logger := GetLogger().Log(level, CLIENT, msg).
		WithCommand(command, cmdID).
		WithClient(cmdID.ClientID).
		WithDuration(duration).
		WithContext("success", success).
		WithContext("result", result)

	if err != nil {
		logger.WithError(err, "client_error")
	}

	logger.WithTags("client", "response").Send()
}

// === Storage Logging ===

func LogKVStoreOperation(replicaID ReplicaID, operation string, key string, value string, success bool, err error, duration time.Duration) {
	if GetLogger() == nil {
		return
	}

	msg := fmt.Sprintf("KV %s operation", operation)
	level := DEBUG
	if !success {
		level = WARN
	}

	logger := GetLogger().Log(level, STORAGE, msg).
		WithKV(operation, key, value).
		WithDuration(duration).
		WithContext("success", success)

	if err != nil {
		logger.WithError(err, "storage_error")
	}

	logger.WithTags("storage", "kv", operation).Send()
}

func LogKVStoreStats(replicaID ReplicaID, totalKeys int, totalOperations int64, avgLatency time.Duration) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, STORAGE, "KV store statistics").
		WithContext("total_keys", totalKeys).
		WithContext("total_operations", totalOperations).
		WithContext("avg_latency_ms", avgLatency.Milliseconds()).
		WithTags("storage", "statistics").
		Send()
}

// === Performance and Metrics Logging ===

func LogPerformanceMetrics(replicaID ReplicaID, metrics map[string]interface{}) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(INFO, GENERAL, "Performance metrics")
	for key, value := range metrics {
		logger.WithContext(key, value)
	}
	logger.WithTags("performance", "metrics").Send()
}

func LogThroughputMetrics(replicaID ReplicaID, commandsPerSecond float64, period time.Duration) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, GENERAL, "Throughput metrics").
		WithContext("commands_per_second", commandsPerSecond).
		WithContext("measurement_period_sec", period.Seconds()).
		WithTags("performance", "throughput").
		Send()
}

func LogLatencyMetrics(replicaID ReplicaID, avgLatency, p50, p95, p99 time.Duration) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(INFO, GENERAL, "Latency metrics").
		WithContext("avg_latency_ms", avgLatency.Milliseconds()).
		WithContext("p50_latency_ms", p50.Milliseconds()).
		WithContext("p95_latency_ms", p95.Milliseconds()).
		WithContext("p99_latency_ms", p99.Milliseconds()).
		WithTags("performance", "latency").
		Send()
}

// === Error and Alert Logging ===

func LogCriticalError(replicaID ReplicaID, component string, err error, context map[string]interface{}) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(ERROR, GENERAL, "Critical error occurred").
		WithError(err, "critical_error").
		WithContext("component", component)

	for key, value := range context {
		logger.WithContext(key, value)
	}

	logger.WithTags("error", "critical", component).Send()
}

func LogConsensusTimeout(replicaID ReplicaID, instanceID int, phase string, timeout time.Duration) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(WARN, CONSENSUS, "Consensus phase timeout").
		WithInstance(int(replicaID), instanceID).
		WithPhase(phase).
		WithContext("timeout_duration_ms", timeout.Milliseconds()).
		WithTags("timeout", "consensus", phase).
		Send()
}

func LogQuorumFailure(replicaID ReplicaID, instanceID int, phase string, received int, required int) {
	if GetLogger() == nil {
		return
	}

	GetLogger().Log(WARN, CONSENSUS, "Quorum failure").
		WithInstance(int(replicaID), instanceID).
		WithPhase(phase).
		WithQuorum(required, received, 0).
		WithTags("quorum", "failure", phase).
		Send()
}

// === Debugging and Development Logging ===

func LogDebugState(replicaID ReplicaID, component string, state map[string]interface{}) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(DEBUG, GENERAL, "Debug state dump").
		WithContext("component", component)

	for key, value := range state {
		logger.WithContext(key, value)
	}

	logger.WithTags("debug", "state", component).Send()
}

func LogInstanceDump(replicaID ReplicaID, instanceID int, instance *EPaxosInstance) {
	if GetLogger() == nil {
		return
	}

	logger := GetLogger().Log(DEBUG, GENERAL, "Instance state dump").
		WithInstance(int(replicaID), instanceID)

	if instance != nil {
		logger.WithCommand(instance.Command, instance.CommandID).
			WithSequence(instance.Seq).
			WithDependencies(instance.Deps).
			WithBallot(instance.Ballot).
			WithStatus(instance.Status).
			WithContext("committed", instance.Committed).
			WithContext("executed", instance.Executed).
			WithContext("timestamp", instance.Timestamp).
			WithContext("leader", instance.Leader).
			WithAttributesUnchanged(instance.AttributesUnchanged)
	}

	logger.WithTags("debug", "instance", "dump").Send()
}
