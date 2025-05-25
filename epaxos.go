package main

import (
	"log"
	"net/rpc"
)

// === PreAccept Phase ===

type PreAcceptArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []int
}

type PreAcceptReply struct {
	OK   bool
	Seq  int
	Deps []int
}

// === Commit Phase ===

type CommitArgs struct {
	ReplicaID  ReplicaID
	InstanceID int
	Command    Command
	CommandID  CommandID
	Seq        int
	Deps       []int
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
	Deps       []int
	Ballot     int
}

type AcceptReply struct {
	OK     bool
	Ballot int
}

// === ReplicaRPC Additions ===

func (r *ReplicaRPC) PreAccept(args PreAcceptArgs, reply *PreAcceptReply) error {
	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
	}

	// === Conflict Detection ===
	maxSeq := args.Seq
	newDeps := make([]int, len(args.Deps))
	copy(newDeps, args.Deps)

	for rid, instanceMap := range r.Replica.Instances {
		for iid, inst := range instanceMap {
			if inst == nil || inst.CommandID == args.CommandID {
				continue
			}
			if commandsConflict(inst.Command, args.Command) {
				// Adjust sequence number
				if inst.Seq >= maxSeq {
					maxSeq = inst.Seq + 1
				}
				// Add dependency on conflicting instance
				if rid == int(r.Replica.ID) {
					newDeps = appendIfMissing(newDeps, iid)
				}
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
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst

	// Reply
	reply.OK = true
	reply.Seq = maxSeq
	reply.Deps = newDeps

	log.Printf("Replica %d: PreAccepted instance %d from %d with Seq=%d, Deps=%v\n",
		r.Replica.ID, args.InstanceID, args.ReplicaID, maxSeq, newDeps)

	return nil
}

func (r *ReplicaRPC) Commit(args CommitArgs, reply *CommitReply) error {
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
	}
	r.Replica.Instances[int(args.ReplicaID)][args.InstanceID] = inst
	r.Replica.InstanceLock.Unlock()

	log.Printf("Replica %d: Committed instance %d from %d\n", r.Replica.ID, args.InstanceID, args.ReplicaID)

	// Try to execute right after committing
	go r.Replica.TryExecute(int(args.ReplicaID), args.InstanceID)

	reply.OK = true
	return nil
}

// === Send PreAccept to Peers ===

func SendPreAcceptToPeer(address string, args PreAcceptArgs) (*PreAcceptReply, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply PreAcceptReply
	err = client.Call("ReplicaRPC.PreAccept", args, &reply)
	if err != nil {
		return nil, err
	}
	return &reply, nil
}

// === Send Commit to Peers ===

func SendCommitToPeer(address string, args CommitArgs) (*CommitReply, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	var reply CommitReply
	err = client.Call("ReplicaRPC.Commit", args, &reply)
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

	// Initial guess
	initialSeq := 1
	initialDeps := []int{}

	args := PreAcceptArgs{
		ReplicaID:  r.ID,
		InstanceID: instanceID,
		Command:    command,
		CommandID:  cmdID,
		Seq:        initialSeq,
		Deps:       initialDeps,
	}

	type replyMeta struct {
		Seq  int
		Deps []int
	}
	replies := []replyMeta{}

	okCount := 1 // Include self
	replies = append(replies, replyMeta{Seq: initialSeq, Deps: initialDeps})

	// Send PreAccept to peers
	for _, peer := range r.Peers {
		reply, err := SendPreAcceptToPeer(peer, args)
		if err != nil {
			log.Printf("PreAccept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			okCount++
			replies = append(replies, replyMeta{Seq: reply.Seq, Deps: reply.Deps})
		}
	}

	// Analyze replies
	same := true
	base := replies[0]
	for _, rep := range replies[1:] {
		if rep.Seq != base.Seq || !equalIntSlice(rep.Deps, base.Deps) {
			same = false
			break
		}
	}

	// Fast path: all replies agree
	if same && okCount > len(r.Peers)/2 {
		log.Println("Fast path: committing directly")

		commitArgs := CommitArgs{
			ReplicaID:  r.ID,
			InstanceID: instanceID,
			Command:    command,
			CommandID:  cmdID,
			Seq:        base.Seq,
			Deps:       base.Deps,
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
		}
		r.InstanceLock.Unlock()
		return nil
	}

	// Slow path: send Accept
	log.Println("Slow path: sending Accept")

	maxSeq := base.Seq
	for _, r := range replies {
		if r.Seq > maxSeq {
			maxSeq = r.Seq
		}
	}

	mergedInput := make([]struct {
		Seq  int
		Deps []int
	}, len(replies))

	for i, r := range replies {
		mergedInput[i] = struct {
			Seq  int
			Deps []int
		}{
			Seq:  r.Seq,
			Deps: r.Deps,
		}
	}

	allDeps := mergeDeps(mergedInput)
	ballot := 1 // You can increment this per conflict

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
	for _, peer := range r.Peers {
		reply, err := SendAcceptToPeer(peer, acceptArgs)
		if err != nil {
			log.Printf("Accept to %s failed: %v", peer, err)
			continue
		}
		if reply.OK {
			ackCount++
		}
	}

	if ackCount > len(r.Peers)/2 {
		log.Println("Accept quorum achieved: committing")

		commitArgs := CommitArgs{
			ReplicaID:  r.ID,
			InstanceID: instanceID,
			Command:    command,
			CommandID:  cmdID,
			Seq:        maxSeq,
			Deps:       allDeps,
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
		}
		r.InstanceLock.Unlock()
	} else {
		log.Println("Accept quorum not achieved. Will retry or abort.")
	}

	return nil
}

func (r *ReplicaRPC) Accept(args AcceptArgs, reply *AcceptReply) error {
	r.Replica.InstanceLock.Lock()
	defer r.Replica.InstanceLock.Unlock()

	if _, ok := r.Replica.Instances[int(args.ReplicaID)]; !ok {
		r.Replica.Instances[int(args.ReplicaID)] = make(map[int]*EPaxosInstance)
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

	log.Printf("Replica %d: Accepted instance %d from %d (Ballot %d)", r.Replica.ID, args.InstanceID, args.ReplicaID, args.Ballot)
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
