package main

import (
	"net/rpc"
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
	OK     bool
	Seq    int
	Deps   []Dependency
	Ballot Ballot
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

				// Add dependency on conflicting instance from ANY replica
				LogDependencyAdded(args.ReplicaID, args.InstanceID, iid)
				newDeps = appendDependencyIfMissing(newDeps, rid, iid)
			}
		}
	}

	// Save the instance
	inst := &EPaxosInstance{
		Command:   args.Command,
		CommandID: args.CommandID,
		Seq:       maxSeq,
		Deps:      newDeps,
		Status:    StatusPreAccepted,
		Ballot:    args.Ballot,
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst

	// Reply
	reply.OK = true
	reply.Seq = maxSeq
	reply.Deps = newDeps
	reply.Ballot = args.Ballot

	LogPreAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, args.Seq, maxSeq, convertDepsToIntSlice(args.Deps), convertDepsToIntSlice(newDeps), reply.OK)

	return nil
}

func (r *ReplicaRPC) Accept(args AcceptArgs, reply *AcceptReply) error {
	LogAcceptPhase(args.ReplicaID, args.InstanceID, args.Seq, convertDepsToIntSlice(args.Deps), args.Ballot.Sequence)

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

	inst := &EPaxosInstance{
		Command:   args.Command,
		CommandID: args.CommandID,
		Seq:       args.Seq,
		Deps:      args.Deps,
		Ballot:    args.Ballot,
		Status:    StatusAccepted,
	}

	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst

	reply.OK = true
	reply.Ballot = args.Ballot

	LogAcceptResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, args.Ballot.Sequence, reply.OK)

	return nil
}

func (r *ReplicaRPC) Commit(args CommitArgs, reply *CommitReply) error {
	LogCommitPhase(args.ReplicaID, args.InstanceID, args.Seq, convertDepsToIntSlice(args.Deps))

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
	r.Replica.InstanceLock.Unlock()

	LogCommitResponse(args.ReplicaID, args.InstanceID, r.Replica.ID, true)

	// Try to execute right after committing
	go r.Replica.TryExecute(int(args.ReplicaID), args.InstanceID)

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

	return nil
}

// === RPC Senders ===

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

func SendAcceptToPeer(address string, args AcceptArgs) (*AcceptReply, error) {
	LogRPCCall(args.ReplicaID, address, "ReplicaRPC.Accept", args)
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply AcceptReply
	err = client.Call("ReplicaRPC.Accept", args, &reply)
	LogRPCReceive(args.ReplicaID, "ReplicaRPC.Accept", args)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

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

func SendPrepareToPeer(address string, args PrepareArgs) (*PrepareReply, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply PrepareReply
	err = client.Call("ReplicaRPC.Prepare", args, &reply)
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

	// Calculate proper fast-path quorum size: F + ⌈(F+1)/2⌉
	f := len(r.Peers) / 2           // Number of tolerated failures
	fastPathQuorum := f + (f+1+1)/2 // ⌈(F+1)/2⌉ = (F+1+1)/2 for integer division

	// Initial guess
	initialSeq := 1
	initialDeps := []Dependency{}

	ballot := Ballot{
		Epoch:     0,
		Sequence:  0,
		ReplicaID: int(r.ID),
	}

	args := PreAcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        initialSeq,
		Deps:       initialDeps,
		Ballot:     ballot,
	}

	replies := []PreAcceptReply{}
	okCount := 1 // Include self
	replies = append(replies, PreAcceptReply{Seq: initialSeq, Deps: initialDeps})

	// Send PreAccept to peers
	for _, peer := range r.Peers {
		reply, err := SendPreAcceptToPeer(peer, args)
		if err != nil {
			GetLogger().Error(PREACCEPT, "PreAccept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			okCount++
			replies = append(replies, *reply)
		}
	}

	// Analyze replies
	same := true
	base := replies[0]
	for _, rep := range replies[1:] {
		if rep.Seq != base.Seq || !equalDependencySlice(rep.Deps, base.Deps) {
			same = false
			break
		}
	}

	// Fast path: all replies agree and we have fast-path quorum
	if same && okCount >= fastPathQuorum {
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
			Command:   command,
			CommandID: cmdID,
			Seq:       base.Seq,
			Deps:      base.Deps,
			Status:    StatusCommitted,
			Committed: true,
			Ballot:    ballot,
		}
		r.InstanceLock.Unlock()
		return nil
	}

	// Slow path: send Accept
	LogSlowPath()
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

	for _, peer := range r.Peers {
		reply, err := SendAcceptToPeer(peer, acceptArgs)
		if err != nil {
			GetLogger().Error(ACCEPT, "Accept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			ackCount++
		}
	}

	if ackCount >= classicQuorum {
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
			Command:   command,
			CommandID: cmdID,
			Seq:       maxSeq,
			Deps:      allDeps,
			Status:    StatusCommitted,
			Committed: true,
			Ballot:    ballot,
		}
		r.InstanceLock.Unlock()
	} else {
		LogAcceptQuorumFailure()
	}

	return nil
}

// ExplicitPrepare implements the recovery protocol from Figure 3
func (r *Replica) ExplicitPrepare(replicaID int, instanceID int) error {
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

	// Send Prepare to all replicas
	for _, peer := range r.Peers {
		reply, err := SendPrepareToPeer(peer, args)
		if err != nil {
			GetLogger().Error(CONSENSUS, "Prepare to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			okCount++
			replies = append(replies, reply)
		}
	}

	f := len(r.Peers) / 2
	if okCount < f+1 {
		return nil // Cannot proceed without majority
	}

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
	} else if highestInstance != nil {
		// Run Accept phase for the highest instance found
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

			for _, peer := range r.Peers {
				go SendCommitToPeer(peer, commitArgs)
			}
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

		for _, peer := range r.Peers {
			go SendCommitToPeer(peer, commitArgs)
		}
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
