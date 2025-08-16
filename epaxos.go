package main

import (
	"fmt"
	"time"
)

// === PreAccept Phase ===

type PreAcceptArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []Dependency
	Ballot     Ballot
}

type PreAcceptReply struct {
	OK                  bool
	Seq                 int
	Deps                []Dependency
	Ballot              Ballot
	AttributesUnchanged bool // NEW: Track if this reply didn't change attributes
}

// === Commit Phase ===

type CommitArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []Dependency
	Ballot     Ballot
}

type CommitReply struct {
	OK bool
}

// === Accept Phase ===

type AcceptArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []Dependency
	Ballot     Ballot
}

type AcceptReply struct {
	OK     bool
	Ballot Ballot
}

// === Prepare Phase (Recovery) ===

type PrepareArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Ballot     Ballot
}

type PrepareReply struct {
	OK        bool
	Ballot    Ballot
	Instance  *EPaxosInstance
	Committed bool
}

// === ReplicaRPC Additions ===

func (r *ReplicaRPC) PreAccept(args PreAcceptArgs, reply *PreAcceptReply) error {

	LogPreAcceptPhase(args.ReplicaID, args.InstanceID, args.Command, args.CommandID)

	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// Check ballot number
	if existingInst, exists := r.Replica.Instances[int(args.ReplicaID)][args.InstanceID]; exists {
		if CompareBallots(args.Ballot, existingInst.Ballot) < 0 {
			reply.OK = false
			reply.Ballot = existingInst.Ballot
			return nil
		}
	}

	// === Conflict Detection ===
	maxSeq := args.Seq
	newDeps := make([]Dependency, len(args.Deps))
	copy(newDeps, args.Deps)

	e := r.Replica.keyIndex[args.Command.Key]
	if e != nil {
		// Which set to scan depends on op type:
		if args.Command.Type == CmdPut {
			// PUT conflicts with writers + readers
			// bump seq against known max on this key
			if e.maxSeq >= maxSeq {
				maxSeq = e.maxSeq + 1
			}

			// add deps for writers
			for _, ik := range e.writers {
				if ik.rid == int(args.ReplicaID) && ik.iid == args.InstanceID {
					continue
				}
				newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
			}
			// add deps for readers (GET vs PUT)
			for _, ik := range e.readers {
				if ik.rid == int(args.ReplicaID) && ik.iid == args.InstanceID {
					continue
				}
				newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
			}

		} else { // GET
			// GET conflicts with writers only
			// sequence bump is only needed relative to conflicting writers
			if e.maxSeq >= maxSeq {
				maxSeq = e.maxSeq + 1
			}

			for _, ik := range e.writers {
				if ik.rid == int(args.ReplicaID) && ik.iid == args.InstanceID {
					continue
				}
				newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
			}
		}
	}

	// NEW: Check if attributes were changed from the leader's proposal
	attributesUnchanged := (maxSeq == args.Seq && equalDependencySlice(newDeps, args.Deps))

	// Save the instance
	inst := &EPaxosInstance{
		Command:             args.Command,
		CommandID:           args.CommandID,
		Seq:                 maxSeq,
		Deps:                newDeps,
		Status:              StatusPreAccepted,
		Ballot:              args.Ballot,
		AttributesUnchanged: attributesUnchanged,
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	r.Replica.indexAdd(inst, int(args.ReplicaID), args.InstanceID)
	// Reply
	reply.OK = true
	reply.Seq = maxSeq
	reply.Deps = newDeps
	reply.Ballot = args.Ballot
	reply.AttributesUnchanged = attributesUnchanged // NEW: Include in reply

	//LogPreAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID,
	//	args.Seq, maxSeq, args.Deps, newDeps, true, attributesUnchanged)

	return nil
}

func (r *ReplicaRPC) Accept(args AcceptArgs, reply *AcceptReply) error {

	LogAcceptPhase(args.ReplicaID, args.InstanceID, args.Seq, args.Deps, args.Ballot)

	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// Check ballot number with detailed logging
	if existingInst, exists := r.Replica.Instances[int(args.ReplicaID)][args.InstanceID]; exists {
		if CompareBallots(args.Ballot, existingInst.Ballot) < 0 {
			reply.OK = false
			reply.Ballot = existingInst.Ballot
			return nil
		}
	}

	inst := &EPaxosInstance{
		Command:   args.Command,
		CommandID: args.CommandID,
		Seq:       args.Seq,
		Deps:      args.Deps,
		Ballot:    args.Ballot,
		Status:    StatusAccepted,
	}

	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	r.Replica.indexAdd(inst, int(args.ReplicaID), args.InstanceID)
	reply.OK = true
	reply.Ballot = args.Ballot

	//LogAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, args.Ballot, true)
	return nil
}

func (r *ReplicaRPC) Commit(args CommitArgs, reply *CommitReply) error {

	LogCommitPhase(args.ReplicaID, args.InstanceID, args.Seq, args.Deps)

	r.Replica.InstanceLock.Lock()
	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	inst := &EPaxosInstance{
		Command:   args.Command,
		CommandID: args.CommandID,
		Seq:       args.Seq,
		Deps:      args.Deps,
		Status:    StatusCommitted,
		Committed: true,
		Ballot:    args.Ballot,
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	r.Replica.indexAdd(inst, int(args.ReplicaID), args.InstanceID)
	r.Replica.InstanceLock.Unlock()

	//LogCommitResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, true)

	// Try to execute right after committing
	r.Replica.onCommitted(int(args.ReplicaID), args.InstanceID)

	reply.OK = true
	return nil
}

func (r *ReplicaRPC) Prepare(args PrepareArgs, reply *PrepareReply) error {
	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	inst, exists := r.Replica.Instances[int(args.ReplicaID)][args.InstanceID]
	if !exists {
		reply.OK = true
		reply.Ballot = args.Ballot
		reply.Instance = nil
		return nil
	}

	// Check ballot number
	if CompareBallots(args.Ballot, inst.Ballot) < 0 {
		reply.OK = false
		reply.Ballot = inst.Ballot
		return nil
	}

	// Update ballot number
	inst.Ballot = args.Ballot

	reply.OK = true
	reply.Ballot = args.Ballot
	reply.Instance = inst
	reply.Committed = inst.Status == StatusCommitted

	//LogPrepareResponse(ReplicaID(r.Replica.ID), int(args.ReplicaID), args.InstanceID,
	//	int(r.Replica.ID), true, reply.Committed, inst)

	return nil
}

// runLocalPreAccept runs PreAccept logic locally and returns the result
func (r *Replica) runLocalPreAccept(command Command, cmdID CommandID, seq int, deps []Dependency, ballot Ballot) (int, []Dependency) {
	r.InstanceLock.RLock()
	defer r.InstanceLock.RUnlock()

	maxSeq := seq
	newDeps := make([]Dependency, len(deps))
	copy(newDeps, deps)

	e := r.keyIndex[command.Key]
	if e == nil {
		return maxSeq, newDeps
	}

	// If you’re okay with a fast upper bound for bumping seq:
	bump := e.maxSeq
	if bump >= maxSeq {
		maxSeq = bump + 1
	}

	if command.Type == CmdPut {
		// PUT conflicts with writers + readers
		for _, ik := range e.writers {
			newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
		}
		for _, ik := range e.readers {
			newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
		}
	} else { // GET
		// GET conflicts only with writers
		for _, ik := range e.writers {
			newDeps = appendDependencyIfMissing(newDeps, ik.rid, ik.iid)
		}
	}

	return maxSeq, newDeps
}

func (r *Replica) Propose(command Command, cmdID CommandID) error {
	startTime := time.Now()

	r.InstanceLock.Lock()
	instanceID := r.NextInstance
	r.NextInstance++
	r.InstanceLock.Unlock()

	IncrementTotalRequests()

	// Calculate proper fast-path quorum size: F + ⌈(F+1)/2⌉
	f := len(r.Peers) / 2         // Number of tolerated failures
	fastPathQuorum := f + (f+1)/2 // ⌈(F+1)/2⌉ = (F+1+1)/2 for integer division

	// Log the proposal start with comprehensive context
	GetLogger().Log(INFO, CONSENSUS, "Starting consensus proposal").
		WithInstance(int(r.ID), instanceID).
		WithCommand(command, cmdID).
		WithContext("fast_path_quorum_required", fastPathQuorum).
		WithContext("total_peers", len(r.Peers)).
		WithContext("failure_tolerance", f).
		WithTags("proposal", "start", "consensus").
		Send()

	// Initial guess
	initialSeq := 1
	initialDeps := []Dependency{}

	ballot := Ballot{
		Epoch:     0,
		Sequence:  0,
		ReplicaID: int(r.ID),
	}

	// Run PreAccept logic locally first
	localSeq, localDeps := r.runLocalPreAccept(command, cmdID, initialSeq, initialDeps, ballot)

	hadLocalConflict := (localSeq != initialSeq) || !equalDependencySlice(localDeps, initialDeps)

	args := PreAcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        localSeq,
		Deps:       localDeps,
		Ballot:     ballot,
	}

	replies := []PreAcceptReply{}
	okCount := 1        // Include self
	unchangedCount := 1 // Self is always "unchanged" relative to local computation

	// Add self reply with local conflict detection results
	replies = append(replies, PreAcceptReply{
		OK:                  true,
		Seq:                 localSeq,
		Deps:                localDeps,
		Ballot:              ballot,
		AttributesUnchanged: true, // Self always unchanged
	})

	// Save the instance locally
	r.InstanceLock.Lock()
	if _, ok := r.Instances[int(r.ID)]; !ok {
		r.Instances[int(r.ID)] = make(map[int]*EPaxosInstance)
	}
	r.Instances[int(r.ID)][instanceID] = &EPaxosInstance{
		Command:             command,
		CommandID:           cmdID,
		Seq:                 localSeq,
		Deps:                localDeps,
		Status:              StatusPreAccepted,
		Ballot:              ballot,
		AttributesUnchanged: true, // Leader's initial proposal is always "unchanged"
	}
	r.InstanceLock.Unlock()

	// Enhanced logging for PreAccept phase
	//GetLogger().Log(INFO, PREACCEPT, "Broadcasting PreAccept to peers").
	//	WithInstance(int(r.ID), instanceID).
	//	WithCommand(command, cmdID).
	//	WithSequence(localSeq).
	//	WithDependencies(localDeps).
	//	WithBallot(ballot).
	//	WithContext("peer_count", len(r.Peers)).
	//	WithTags("preaccept", "broadcast").
	//	Send()

	// Send PreAccept to ALL peers (redundant PreAccepts)
	for i, peer := range r.Peers {
		peerStartTime := time.Now()
		reply, err := SendPreAcceptToPeer(peer, args)
		peerDuration := time.Since(peerStartTime)

		if err != nil {
			GetLogger().Log(ERROR, PREACCEPT, "PreAccept RPC failed").
				WithInstance(int(r.ID), instanceID).
				WithRPC(peer, "ReplicaRPC.PreAccept", peerDuration, false).
				WithError(err, "rpc_error").
				WithContext("peer_index", i).
				WithTags("preaccept", "rpc", "failure").
				Send()
			continue
		}

		if reply.OK {
			okCount++
			replies = append(replies, *reply)
			if reply.AttributesUnchanged {
				unchangedCount++
			}

			// Log successful PreAccept response with detailed analysis
			LogPreAcceptResponse(r.ID, instanceID, ReplicaID(i),
				localSeq, reply.Seq, localDeps, reply.Deps,
				true, reply.AttributesUnchanged)

		} else {
			LogPreAcceptResponse(r.ID, instanceID, ReplicaID(i),
				localSeq, 0, localDeps, []Dependency{},
				false, false)
		}
	}

	// Analyze replies for fast path
	same := true
	base := replies[0]
	for _, rep := range replies[1:] {
		if rep.Seq != base.Seq || !equalDependencySlice(rep.Deps, base.Deps) {
			same = false
			break
		}
	}

	hadReplyConflict := !same

	if hadLocalConflict || hadReplyConflict {
		IncrementConflictDetect() // counts once per request, on the leader only
	}

	// Enhanced fast path condition for redundant PreAccepts
	canUseFastPath := same && unchangedCount >= fastPathQuorum

	if canUseFastPath {
		// Enhanced fast path logging

		IncrementFastPath()

		commitArgs := CommitArgs{
			ReplicaID:  r.ID,
			InstanceID: instanceID,
			Command:    command,
			CommandID:  cmdID,
			Seq:        base.Seq,
			Deps:       base.Deps,
			Ballot:     ballot,
		}

		for _, peer := range r.Peers {
			go SendCommitToPeer(peer, commitArgs)
		}

		// Update local instance to committed
		r.InstanceLock.Lock()
		if inst, exists := r.Instances[int(r.ID)][instanceID]; exists {
			inst.Seq = base.Seq
			inst.Deps = base.Deps
			inst.Status = StatusCommitted
			inst.Committed = true
		}
		r.InstanceLock.Unlock()

		r.onCommitted(int(r.ID), instanceID)

		LogFastPath(r.ID, instanceID, fastPathQuorum, okCount, unchangedCount, command, cmdID)

		return nil
	}

	IncrementSlowPath()

	// Slow path with enhanced logging
	reason := fmt.Sprintf("unchanged_responses=%d, required=%d, identical=%v",
		unchangedCount-1, fastPathQuorum-1, same)

	maxSeq := getMaxSeq(replies)
	allDeps := mergeDependencies(replies)

	ballot.Sequence = 1 // Increment for Accept phase

	acceptArgs := AcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        maxSeq,
		Deps:       allDeps,
		Ballot:     ballot,
	}

	ackCount := 1          // self
	classicQuorum := f + 1 // Classic Paxos quorum

	acceptStart := time.Now()
	LogAcceptPhase(r.ID, instanceID, maxSeq, allDeps, ballot)

	for i, peer := range r.Peers {
		peerStartTime := time.Now()
		reply, err := SendAcceptToPeer(peer, acceptArgs)
		peerDuration := time.Since(peerStartTime)

		if err != nil {
			GetLogger().Log(ERROR, ACCEPT, "Accept RPC failed").
				WithInstance(int(r.ID), instanceID).
				WithRPC(peer, "ReplicaRPC.Accept", peerDuration, false).
				WithError(err, "rpc_error").
				WithContext("peer_index", i).
				WithTags("accept", "rpc", "failure").
				Send()
			continue
		}

		if reply.OK {
			ackCount++
			LogAcceptResponse(r.ID, instanceID, ReplicaID(i), reply.Ballot, true)
		} else {
			LogAcceptResponse(r.ID, instanceID, ReplicaID(i), reply.Ballot, false)
		}
	}

	acceptDuration := time.Since(acceptStart)

	if ackCount >= classicQuorum {
		LogAcceptQuorum(r.ID, instanceID, ackCount, classicQuorum)

		commitArgs := CommitArgs{
			ReplicaID:  r.ID,
			InstanceID: instanceID,
			Command:    command,
			CommandID:  cmdID,
			Seq:        maxSeq,
			Deps:       allDeps,
			Ballot:     ballot,
		}

		for _, peer := range r.Peers {
			go SendCommitToPeer(peer, commitArgs)
		}

		// Update local instance to committed
		r.InstanceLock.Lock()
		if inst, exists := r.Instances[int(r.ID)][instanceID]; exists {
			inst.Seq = maxSeq
			inst.Deps = allDeps
			inst.Status = StatusCommitted
			inst.Committed = true
			inst.Ballot = ballot
		}
		r.InstanceLock.Unlock()

		totalDuration := time.Since(startTime)
		GetLogger().Log(INFO, CONSENSUS, "Slow path consensus completed").
			WithInstance(int(r.ID), instanceID).
			WithCommand(command, cmdID).
			WithDuration(totalDuration).
			WithContext("accept_phase_duration_ms", acceptDuration.Milliseconds()).
			WithTags("slow_path", "completed", "success").
			Send()

	} else {
		LogAcceptQuorumFailure(r.ID, instanceID, ackCount, classicQuorum)

		totalDuration := time.Since(startTime)
		GetLogger().Log(ERROR, CONSENSUS, "Consensus failed - insufficient Accept responses").
			WithInstance(int(r.ID), instanceID).
			WithCommand(command, cmdID).
			WithQuorum(classicQuorum, ackCount, 0).
			WithDuration(totalDuration).
			WithContext("accept_phase_duration_ms", acceptDuration.Milliseconds()).
			WithError(fmt.Errorf("insufficient accept responses: %d/%d", ackCount, classicQuorum), "consensus_failure").
			WithTags("slow_path", "failed", "quorum_failure").
			Send()
	}

	r.onCommitted(int(r.ID), instanceID)
	LogSlowPath(r.ID, instanceID, reason)

	return nil
}

// NEW: Helper function to filter instances that have unchanged attributes
func filterUnchangedInstances(replies []*PrepareReply) []*PrepareReply {
	var filtered []*PrepareReply
	for _, reply := range replies {
		if reply.Instance != nil && reply.Instance.AttributesUnchanged {
			filtered = append(filtered, reply)
		}
	}
	return filtered
}

// NEW: Check if all unchanged instances have identical attributes
func allUnchangedInstancesMatch(unchangedReplies []*PrepareReply) bool {
	if len(unchangedReplies) <= 1 {
		return true
	}

	base := unchangedReplies[0].Instance
	for _, reply := range unchangedReplies[1:] {
		inst := reply.Instance
		if inst.Seq != base.Seq || !equalDependencySlice(inst.Deps, base.Deps) {
			return false
		}
	}
	return true
}

// ExplicitPrepare implements the recovery protocol from Figure 3 with redundant PreAccepts support
func (r *Replica) ExplicitPrepare(replicaID int, instanceID int) error {
	recoveryStart := time.Now()

	LogRecoveryStart(r.ID, replicaID, instanceID, "explicit_prepare", 1)

	r.InstanceLock.Lock()
	ballot := Ballot{
		Epoch:     0,
		Sequence:  1,
		ReplicaID: int(r.ID),
	}
	r.InstanceLock.Unlock()

	args := PrepareArgs{
		ReplicaID:  ReplicaID(replicaID),
		InstanceID: instanceID,
		Ballot:     ballot,
	}

	replies := []*PrepareReply{}
	okCount := 0

	// Send Prepare to all replicas (including self through RPC for consistency)
	allPeers := append(r.Peers, fmt.Sprintf("localhost:%d", 8000+int(r.ID)))
	for _, peer := range allPeers {
		reply, err := SendPrepareToPeer(peer, args)
		if err != nil {
			GetLogger().Log(ERROR, RECOVERY, "Prepare RPC failed").
				WithInstance(replicaID, instanceID).
				WithRPC(peer, "ReplicaRPC.Prepare", 0, false).
				WithError(err, "rpc_error").
				WithTags("recovery", "prepare", "rpc_failure").
				Send()
			continue
		}
		if reply.OK {
			okCount++
			replies = append(replies, reply)
		}
	}

	f := len(r.Peers) / 2
	if okCount < f+1 {
		GetLogger().Log(WARN, RECOVERY, "Recovery failed - insufficient Prepare replies").
			WithInstance(replicaID, instanceID).
			WithQuorum(f+1, okCount, 0).
			WithDuration(time.Since(recoveryStart)).
			WithTags("recovery", "failed", "insufficient_quorum").
			Send()
		return nil // Cannot proceed without majority
	}

	GetLogger().Log(DEBUG, RECOVERY, "Prepare quorum achieved").
		WithInstance(replicaID, instanceID).
		WithQuorum(f+1, okCount, 0).
		WithContext("total_replies", len(replies)).
		WithTags("recovery", "prepare", "quorum").
		Send()

	// Find the highest ballot among replies
	var highestInstance *EPaxosInstance
	committed := false

	for _, reply := range replies {
		if reply.Instance != nil {
			if reply.Committed {
				committed = true
				highestInstance = reply.Instance
				break
			}
			if highestInstance == nil || CompareBallots(reply.Instance.Ballot, highestInstance.Ballot) > 0 {
				highestInstance = reply.Instance
			}
		}
	}

	if committed && highestInstance != nil {
		// Instance is already committed, just commit locally
		GetLogger().Log(INFO, RECOVERY, "Instance already committed during recovery").
			WithInstance(replicaID, instanceID).
			WithCommand(highestInstance.Command, highestInstance.CommandID).
			WithSequence(highestInstance.Seq).
			WithDependencies(highestInstance.Deps).
			WithTags("recovery", "already_committed").
			Send()

		commitArgs := CommitArgs{
			ReplicaID:  ReplicaID(replicaID),
			InstanceID: instanceID,
			Command:    highestInstance.Command,
			CommandID:  highestInstance.CommandID,
			Seq:        highestInstance.Seq,
			Deps:       highestInstance.Deps,
			Ballot:     highestInstance.Ballot,
		}

		r.InstanceLock.Lock()
		if _, ok := r.Instances[replicaID]; !ok {
			r.Instances[replicaID] = make(map[int]*EPaxosInstance)
		}
		r.Instances[replicaID][instanceID] = &EPaxosInstance{
			Command:   highestInstance.Command,
			CommandID: highestInstance.CommandID,
			Seq:       highestInstance.Seq,
			Deps:      highestInstance.Deps,
			Status:    StatusCommitted,
			Committed: true,
			Ballot:    highestInstance.Ballot,
		}
		r.InstanceLock.Unlock()

		// Notify other replicas
		for _, peer := range r.Peers {
			go SendCommitToPeer(peer, commitArgs)
		}

		LogRecoveryComplete(r.ID, replicaID, instanceID, true, time.Since(recoveryStart))
		return nil
	}

	if highestInstance != nil {
		// Check if this could have been committed on fast path with redundant PreAccepts
		unchangedReplies := filterUnchangedInstances(replies)
		fastPathQuorum := f + (f+1)/2 // F + ⌈(F+1)/2⌉

		GetLogger().Log(INFO, RECOVERY, "Analyzing recovery for fast-path eligibility").
			WithInstance(replicaID, instanceID).
			WithContext("unchanged_replies", len(unchangedReplies)).
			WithContext("total_replies", len(replies)).
			WithContext("fast_path_quorum_required", fastPathQuorum-1).
			WithTags("recovery", "fast_path", "analysis").
			Send()

		// If we have enough unchanged replies with identical attributes,
		// this instance could have been fast-path committed
		if len(unchangedReplies) >= fastPathQuorum-1 && allUnchangedInstancesMatch(unchangedReplies) {
			GetLogger().Log(INFO, RECOVERY, "Fast-path committing during recovery").
				WithInstance(replicaID, instanceID).
				WithCommand(highestInstance.Command, highestInstance.CommandID).
				WithSequence(highestInstance.Seq).
				WithDependencies(highestInstance.Deps).
				WithContext("unchanged_replies", len(unchangedReplies)).
				WithTags("recovery", "fast_path", "commit").
				Send()

			// This instance was likely fast-path committed, commit directly
			commitArgs := CommitArgs{
				ReplicaID:  ReplicaID(replicaID),
				InstanceID: instanceID,
				Command:    highestInstance.Command,
				CommandID:  highestInstance.CommandID,
				Seq:        highestInstance.Seq,
				Deps:       highestInstance.Deps,
				Ballot:     ballot,
			}

			// Update local state first
			r.InstanceLock.Lock()
			if _, ok := r.Instances[replicaID]; !ok {
				r.Instances[replicaID] = make(map[int]*EPaxosInstance)
			}
			r.Instances[replicaID][instanceID] = &EPaxosInstance{
				Command:   highestInstance.Command,
				CommandID: highestInstance.CommandID,
				Seq:       highestInstance.Seq,
				Deps:      highestInstance.Deps,
				Status:    StatusCommitted,
				Committed: true,
				Ballot:    ballot,
			}
			r.InstanceLock.Unlock()

			// Notify other replicas
			for _, peer := range r.Peers {
				go SendCommitToPeer(peer, commitArgs)
			}

			LogRecoveryComplete(r.ID, replicaID, instanceID, true, time.Since(recoveryStart))
			return nil
		}

		// Fall back to Accept phase for regular recovery
		GetLogger().Log(INFO, RECOVERY, "Using Accept phase for recovery").
			WithInstance(replicaID, instanceID).
			WithCommand(highestInstance.Command, highestInstance.CommandID).
			WithContext("unchanged_replies", len(unchangedReplies)).
			WithContext("required_unchanged", fastPathQuorum-1).
			WithContext("reason", "insufficient_unchanged_replies").
			WithTags("recovery", "accept_phase", "fallback").
			Send()

		acceptArgs := AcceptArgs{
			ReplicaID:  ReplicaID(replicaID),
			InstanceID: instanceID,
			Command:    highestInstance.Command,
			CommandID:  highestInstance.CommandID,
			Seq:        highestInstance.Seq,
			Deps:       highestInstance.Deps,
			Ballot:     ballot,
		}

		ackCount := 0
		for _, peer := range r.Peers {
			reply, err := SendAcceptToPeer(peer, acceptArgs)
			if err != nil {
				GetLogger().Log(ERROR, RECOVERY, "Accept RPC failed during recovery").
					WithInstance(replicaID, instanceID).
					WithRPC(peer, "ReplicaRPC.Accept", 0, false).
					WithError(err, "rpc_error").
					WithTags("recovery", "accept", "rpc_failure").
					Send()
				continue
			}
			if reply.OK {
				ackCount++
			}
		}

		if ackCount >= f+1 {

			// Commit the instance
			commitArgs := CommitArgs{
				ReplicaID:  ReplicaID(replicaID),
				InstanceID: instanceID,
				Command:    highestInstance.Command,
				CommandID:  highestInstance.CommandID,
				Seq:        highestInstance.Seq,
				Deps:       highestInstance.Deps,
				Ballot:     ballot,
			}

			// Update local state
			r.InstanceLock.Lock()
			if _, ok := r.Instances[replicaID]; !ok {
				r.Instances[replicaID] = make(map[int]*EPaxosInstance)
			}
			r.Instances[replicaID][instanceID] = &EPaxosInstance{
				Command:   highestInstance.Command,
				CommandID: highestInstance.CommandID,
				Seq:       highestInstance.Seq,
				Deps:      highestInstance.Deps,
				Status:    StatusCommitted,
				Committed: true,
				Ballot:    ballot,
			}
			r.InstanceLock.Unlock()

			// Notify other replicas
			for _, peer := range r.Peers {
				go SendCommitToPeer(peer, commitArgs)
			}

			LogRecoveryComplete(r.ID, replicaID, instanceID, true, time.Since(recoveryStart))

		} else {
			LogRecoveryComplete(r.ID, replicaID, instanceID, false, time.Since(recoveryStart))
		}
	} else {
		// No instance found, commit no-op

		noOpCommand := Command{Type: CmdGet, Key: "__noop__", Value: ""}
		noOpCmdID := CommandID{ClientID: "system", SeqNum: instanceID}

		commitArgs := CommitArgs{
			ReplicaID:  ReplicaID(replicaID),
			InstanceID: instanceID,
			Command:    noOpCommand,
			CommandID:  noOpCmdID,
			Seq:        0,
			Deps:       []Dependency{},
			Ballot:     ballot,
		}

		// Update local state
		r.InstanceLock.Lock()
		if _, ok := r.Instances[replicaID]; !ok {
			r.Instances[replicaID] = make(map[int]*EPaxosInstance)
		}
		r.Instances[replicaID][instanceID] = &EPaxosInstance{
			Command:   noOpCommand,
			CommandID: noOpCmdID,
			Seq:       0,
			Deps:      []Dependency{},
			Status:    StatusCommitted,
			Committed: true,
			Ballot:    ballot,
		}
		r.InstanceLock.Unlock()

		// Notify other replicas
		for _, peer := range r.Peers {
			go SendCommitToPeer(peer, commitArgs)
		}

		LogRecoveryComplete(r.ID, replicaID, instanceID, true, time.Since(recoveryStart))
	}

	return nil
}

// Helper function to convert dependencies to int slice for logging compatibility
func convertDepsToIntSlice(deps []Dependency) []int {
	result := make([]int, len(deps))
	for i, dep := range deps {
		result[i] = dep.InstanceID // For logging purposes, just use instance ID
	}
	return result
}
