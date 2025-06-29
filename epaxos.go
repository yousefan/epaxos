package main

import (
	"fmt"
	"net/rpc"
	"time"
)

// === PreAccept Phase ===

type PreAcceptArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []CrossReplicaDependency
	Ballot     Ballot
}

type PreAcceptReply struct {
	OK     bool
	Seq    int
	Deps   []CrossReplicaDependency
	Ballot Ballot
}

// === Commit Phase ===

type CommitArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []CrossReplicaDependency
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
	Deps       []CrossReplicaDependency
	Ballot     Ballot
}

type AcceptReply struct {
	OK     bool
	Ballot Ballot
}

// === ReplicaRPC Additions ===

func (r *ReplicaRPC) PreAccept(args PreAcceptArgs, reply *PreAcceptReply) error {
	LogPreAcceptPhase(args.ReplicaID, args.InstanceID, args.Command, args.CommandID)

	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	// Update ballot if necessary
	if !r.Replica.UpdateBallot(args.Ballot) {
		reply.OK = false
		return nil
	}

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// === Conflict Detection ===
	maxSeq := args.Seq
	newDeps := make([]CrossReplicaDependency, len(args.Deps))
	copy(newDeps, args.Deps)
	dependencySet := &DependencySet{}

	// Check for conflicts across all replicas
	for rid, instanceMap := range r.Replica.Instances {
		for iid, inst := range instanceMap {
			if inst == nil || inst.CommandID == args.CommandID {
				continue
			}
			if commandsConflict(inst.Command, args.Command) {
				LogConflictDetection(args.ReplicaID, args.InstanceID, rid, iid, args.Command, inst.Command)
				// Adjust sequence number
				if inst.Seq >= maxSeq {
					maxSeq = inst.Seq + 1
				}
				// Add dependency on conflicting instance
				dependencySet.AddDependency(ReplicaID(rid), iid)
				if rid == int(r.Replica.ID) {
					LogDependencyAdded(args.ReplicaID, args.InstanceID, iid)
					newDeps = appendIfMissingCrossReplica(newDeps, CrossReplicaDependency{ReplicaID: ReplicaID(rid), InstanceID: iid})
				}
			}
		}
	}

	// Save the instance
	inst := &EPaxosInstance{
		Command:       args.Command,
		CommandID:     args.CommandID,
		Seq:           maxSeq,
		Deps:          newDeps,
		Status:        StatusPreAccepted,
		Ballot:        args.Ballot,
		Leader:        args.ReplicaID,
		Quorum:        r.Replica.QuorumSize,
		PreAcceptedBy: []ReplicaID{r.Replica.ID},
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst

	// Reply
	reply.OK = true
	reply.Seq = maxSeq
	reply.Deps = newDeps
	reply.Ballot = r.Replica.CurrentBallot

	LogPreAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, args.Seq, maxSeq, args.Deps, newDeps, reply.OK)

	return nil
}

func (r *ReplicaRPC) Commit(args CommitArgs, reply *CommitReply) error {
	LogCommitPhase(args.ReplicaID, args.InstanceID, args.Seq, args.Deps)

	r.Replica.InstanceLock.Lock()
	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// Update ballot if necessary
	r.Replica.UpdateBallot(args.Ballot)

	inst := &EPaxosInstance{
		Command:     args.Command,
		CommandID:   args.CommandID,
		Seq:         args.Seq,
		Deps:        args.Deps,
		Status:      StatusCommitted,
		Committed:   true,
		Ballot:      args.Ballot,
		Leader:      args.ReplicaID,
		Quorum:      r.Replica.QuorumSize,
		CommittedBy: []ReplicaID{r.Replica.ID},
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	r.Replica.InstanceLock.Unlock()

	LogCommitResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, true)

	// Try to execute right after committing
	go r.Replica.TryExecute(int(args.ReplicaID), args.InstanceID)

	reply.OK = true
	return nil
}

// === Send PreAccept to Peers ===

func SendPreAcceptToPeer(address string, args PreAcceptArgs) (*PreAcceptReply, error) {
	LogRPCCall(args.ReplicaID, address, "ReplicaRPC.PreAccept", args)
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply PreAcceptReply
	err = client.Call("ReplicaRPC.PreAccept", args, &reply)
	LogRPCReceive(args.ReplicaID, "ReplicaRPC.PreAccept", args)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

// === Send Commit to Peers ===

func SendCommitToPeer(address string, args CommitArgs) (*CommitReply, error) {
	LogRPCCall(args.ReplicaID, address, "ReplicaRPC.Commit", args)
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply CommitReply
	err = client.Call("ReplicaRPC.Commit", args, &reply)
	LogRPCReceive(args.ReplicaID, "ReplicaRPC.Commit", args)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

func (r *Replica) Propose(command Command, cmdID CommandID) error {
	r.InstanceLock.Lock()
	instanceID := r.NextInstance
	r.NextInstance++
	r.InstanceLock.Unlock()

	// Check for duplicate client request
	clientReq := &ClientRequest{
		CommandID: cmdID,
		Command:   command,
		Timestamp: time.Now(),
	}

	if !r.TrackClientRequest(clientReq) {
		GetLogger().Warn(CLIENT, "Duplicate client request detected: %s", formatCommandID(cmdID))
		// Return existing result if available
		if _, exists := r.GetClientRequest(cmdID); exists {
			// TODO: Return cached result
			return nil
		}
	}

	// Try proposal with exponential backoff
	maxRetries := 5
	backoff := time.Millisecond * 100

	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := r.tryPropose(command, cmdID, instanceID); err == nil {
			return nil // Success
		}

		// Exponential backoff
		time.Sleep(backoff)
		backoff *= 2

		GetLogger().Warn(CONSENSUS, "Proposal attempt %d failed, retrying with backoff %v", attempt+1, backoff)
	}

	return fmt.Errorf("failed to propose command after %d attempts", maxRetries)
}

func (r *Replica) tryPropose(command Command, cmdID CommandID, instanceID int) error {
	// Generate ballot for this proposal
	ballot := r.GetNextBallot()

	// Initial guess
	initialSeq := 1
	initialDeps := []CrossReplicaDependency{}

	args := PreAcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        initialSeq,
		Deps:       initialDeps,
		Ballot:     ballot,
	}

	replies := []struct {
		Seq    int
		Deps   []CrossReplicaDependency
		Ballot Ballot
		OK     bool
	}{}

	okCount := 1 // Include self
	replies = append(replies, struct {
		Seq    int
		Deps   []CrossReplicaDependency
		Ballot Ballot
		OK     bool
	}{Seq: initialSeq, Deps: initialDeps, Ballot: ballot, OK: true})

	// Send PreAccept to peers
	for _, peer := range r.Peers {
		reply, err := SendPreAcceptToPeer(peer, args)
		if err != nil {
			GetLogger().Error(PREACCEPT, "PreAccept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			okCount++
			replies = append(replies, struct {
				Seq    int
				Deps   []CrossReplicaDependency
				Ballot Ballot
				OK     bool
			}{Seq: reply.Seq, Deps: reply.Deps, Ballot: reply.Ballot, OK: true})
		} else {
			replies = append(replies, struct {
				Seq    int
				Deps   []CrossReplicaDependency
				Ballot Ballot
				OK     bool
			}{OK: false})
		}
	}

	// Analyze replies
	same := true
	base := replies[0]
	for _, rep := range replies[1:] {
		if !rep.OK || rep.Seq != base.Seq || !equalCrossReplicaDeps(rep.Deps, base.Deps) {
			same = false
			break
		}
	}

	// Fast path: all replies agree and we have a fast-path quorum
	if same && okCount >= r.FastPathQuorum {
		LogFastPath()
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

		// Save locally
		r.InstanceLock.Lock()
		if _, ok := r.Instances[int(r.ID)]; !ok {
			r.Instances[int(r.ID)] = make(map[int]*EPaxosInstance)
		}
		r.Instances[int(r.ID)][instanceID] = &EPaxosInstance{
			Command:     command,
			CommandID:   cmdID,
			Seq:         base.Seq,
			Deps:        base.Deps,
			Status:      StatusCommitted,
			Committed:   true,
			Ballot:      ballot,
			Leader:      r.ID,
			Quorum:      r.QuorumSize,
			CommittedBy: []ReplicaID{r.ID},
		}
		r.InstanceLock.Unlock()
		return nil
	}

	// Slow path: send Accept with retry logic
	return r.trySlowPath(command, cmdID, instanceID, ballot, replies)
}

func (r *Replica) trySlowPath(command Command, cmdID CommandID, instanceID int, ballot Ballot, replies []struct {
	Seq    int
	Deps   []CrossReplicaDependency
	Ballot Ballot
	OK     bool
}) error {
	LogSlowPath()
	maxSeq := replies[0].Seq
	for _, rep := range replies {
		if rep.OK && rep.Seq > maxSeq {
			maxSeq = rep.Seq
		}
	}

	mergedInput := make([]struct {
		Seq  int
		Deps []CrossReplicaDependency
	}, len(replies))

	for i, rep := range replies {
		if rep.OK {
			mergedInput[i] = struct {
				Seq  int
				Deps []CrossReplicaDependency
			}{
				Seq:  rep.Seq,
				Deps: rep.Deps,
			}
		}
	}

	allDeps := mergeCrossReplicaDeps(mergedInput)

	acceptArgs := AcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        maxSeq,
		Deps:       allDeps,
		Ballot:     ballot,
	}

	ackCount := 1 // self
	var nacks []Ballot

	for _, peer := range r.Peers {
		reply, err := SendAcceptToPeer(peer, acceptArgs)
		if err != nil {
			GetLogger().Error(ACCEPT, "Accept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			ackCount++
		} else {
			// Collect NACKs for potential re-proposal
			nacks = append(nacks, reply.Ballot)
		}
	}

	if r.IsQuorum(ackCount) {
		LogAcceptQuorum()
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

		r.InstanceLock.Lock()
		if _, ok := r.Instances[int(r.ID)]; !ok {
			r.Instances[int(r.ID)] = make(map[int]*EPaxosInstance)
		}
		r.Instances[int(r.ID)][instanceID] = &EPaxosInstance{
			Command:     command,
			CommandID:   cmdID,
			Seq:         maxSeq,
			Deps:        allDeps,
			Status:      StatusCommitted,
			Committed:   true,
			Ballot:      ballot,
			Leader:      r.ID,
			Quorum:      r.QuorumSize,
			CommittedBy: []ReplicaID{r.ID},
		}
		r.InstanceLock.Unlock()
		return nil
	} else {
		LogAcceptQuorumFailure()

		// Update ballot based on NACKs
		for _, nackBallot := range nacks {
			if nackBallot.Less(r.CurrentBallot) {
				r.UpdateBallot(nackBallot)
			}
		}

		return fmt.Errorf("accept quorum not achieved")
	}
}

func (r *ReplicaRPC) Accept(args AcceptArgs, reply *AcceptReply) error {
	LogAcceptPhase(args.ReplicaID, args.InstanceID, args.Seq, args.Deps, args.Ballot.Number)

	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	// Check if we have a higher ballot number
	if !r.Replica.UpdateBallot(args.Ballot) {
		// We have a higher ballot - NACK this request
		reply.OK = false
		reply.Ballot = r.Replica.CurrentBallot
		GetLogger().Warn(ACCEPT, "NACK: Replica %d has higher ballot %v than request ballot %v",
			r.Replica.ID, r.Replica.CurrentBallot, args.Ballot)
		return nil
	}

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// Check if instance already exists with higher ballot
	if existingInst, exists := r.Replica.Instances[int(args.ReplicaID)][args.InstanceID]; exists {
		if existingInst.Ballot.Less(args.Ballot) {
			// Update existing instance with new ballot
			existingInst.Ballot = args.Ballot
			existingInst.Command = args.Command
			existingInst.CommandID = args.CommandID
			existingInst.Seq = args.Seq
			existingInst.Deps = args.Deps
			existingInst.Status = StatusAccepted
			existingInst.Leader = args.ReplicaID
			existingInst.AcceptedBy = append(existingInst.AcceptedBy, r.Replica.ID)
		} else if args.Ballot.Less(existingInst.Ballot) {
			// Request has lower ballot - NACK
			reply.OK = false
			reply.Ballot = existingInst.Ballot
			GetLogger().Warn(ACCEPT, "NACK: Instance R%d.%d has higher ballot %v than request ballot %v",
				args.ReplicaID, args.InstanceID, existingInst.Ballot, args.Ballot)
			return nil
		}
	} else {
		// Create new instance
		inst := &EPaxosInstance{
			Command:    args.Command,
			CommandID:  args.CommandID,
			Seq:        args.Seq,
			Deps:       args.Deps,
			Ballot:     args.Ballot,
			Status:     StatusAccepted,
			Leader:     args.ReplicaID,
			Quorum:     r.Replica.QuorumSize,
			AcceptedBy: []ReplicaID{r.Replica.ID},
		}
		r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	}

	reply.OK = true
	reply.Ballot = r.Replica.CurrentBallot

	LogAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, args.Ballot.Number, reply.OK)

	return nil
}

func SendAcceptToPeer(address string, args AcceptArgs) (*AcceptReply, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply AcceptReply
	err = client.Call("ReplicaRPC.Accept", args, &reply)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}
