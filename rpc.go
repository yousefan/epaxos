package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"sync"
	"time"
)

// === RPC Argument and Reply Types ===

// Put/Get request from clients
type ClientRequest struct {
	Command      Command
	CommandID    CommandID // Added command ID field
	CommandCount int       // Added command count field
}

type ClientReply struct {
	Success bool
	Value   string // Only relevant for GET
	Error   string
}

// === RPC Handler (exposed by replica) ===

type ReplicaRPC struct {
	Replica *Replica
}

func (r *ReplicaRPC) ClientPropose(req ClientRequest, reply *ClientReply) error {
	// Log client request using the LogClientRequest function like in main.go
	LogClientRequest(r.Replica.ID, req.Command, req.CommandID, req.CommandCount)

	start := time.Now()

	// Use the Propose method instead of directly manipulating KVStore
	err := r.Replica.Propose(req.Command, req.CommandID)
	duration := time.Since(start)

	if err != nil {
		GetLogger().Log(ERROR, CLIENT, "Client request failed").
			WithCommand(req.Command, req.CommandID).
			WithError(err, "proposal_error").
			WithDuration(duration).
			WithContext("replica_id", r.Replica.ID).
			WithContext("command_count", req.CommandCount).
			WithTags("rpc", "client_request", "failed", string(req.Command.Type)).
			Send()

		reply.Success = false
		reply.Error = err.Error()
		return nil
	}

	// For GET commands, we need to wait a bit and then read the result
	if req.Command.Type == CmdGet {
		// Wait a moment for execution to complete, then read the result
		time.Sleep(100 * time.Millisecond)
		val, ok := r.Replica.KVStore.Get(req.Command.Key)

		GetLogger().Log(INFO, CLIENT, "GET request completed").
			WithCommand(req.Command, req.CommandID).
			WithDuration(duration).
			WithContext("replica_id", r.Replica.ID).
			WithContext("command_count", req.CommandCount).
			WithContext("value_found", ok).
			WithContext("value", val).
			WithTags("rpc", "client_request", "success", "get").
			Send()

		if !ok {
			reply.Success = false
			reply.Error = "Key not found"
		} else {
			reply.Success = true
			reply.Value = val
		}
	} else {
		// For PUT commands
		GetLogger().Log(INFO, CLIENT, "PUT request completed").
			WithCommand(req.Command, req.CommandID).
			WithDuration(duration).
			WithContext("replica_id", r.Replica.ID).
			WithContext("command_count", req.CommandCount).
			WithTags("rpc", "client_request", "success", "put").
			Send()

		reply.Success = true
	}

	return nil
}

// === Server Initialization ===

func StartRPCServer(replica *Replica, address string) error {
	rpcHandler := &ReplicaRPC{Replica: replica}
	err := rpc.Register(rpcHandler)
	if err != nil {
		return fmt.Errorf("error registering RPC handler: %w", err)
	}

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}
	log.Printf("Replica %d listening on %s\n", replica.ID, address)
	go rpc.Accept(listener)
	return nil
}

// === Client Call Utility (used by external client binaries) ===

func SendClientCommand(address string, cmd Command, commandCount int) (*ClientReply, error) {
	// Note: This is used by non-replica clients; it can remain one-off.
	// Your load generator now pools on its side anyway.
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to replica: %w", err)
	}
	defer client.Close()

	// Generate a command ID for the request
	cmdID := CommandID{
		ClientID: "remote_client",
		SeqNum:   time.Now().Nanosecond(),
	}

	req := ClientRequest{
		Command:      cmd,
		CommandID:    cmdID,
		CommandCount: commandCount,
	}
	var reply ClientReply

	// Update RPC method name to ClientPropose
	err = client.Call("ReplicaRPC.ClientPropose", req, &reply)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}
	return &reply, nil
}

// ============================================================================
//                         PERSISTENT RPC CLIENT POOL
// ============================================================================

type rpcClientPool struct {
	mu      sync.RWMutex
	clients map[string]*rpc.Client
}

var pool rpcClientPool

func (p *rpcClientPool) get(address string) (*rpc.Client, error) {
	p.mu.RLock()
	c := p.clients[address]
	p.mu.RUnlock()
	if c != nil {
		return c, nil
	}

	// Dial under write lock (double-check)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.clients == nil {
		p.clients = make(map[string]*rpc.Client)
	}
	if c = p.clients[address]; c != nil {
		return c, nil
	}
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, err
	}
	p.clients[address] = client
	return client, nil
}

func (p *rpcClientPool) invalidate(address string) {
	p.mu.Lock()
	if p.clients == nil {
		p.mu.Unlock()
		return
	}
	if c, ok := p.clients[address]; ok && c != nil {
		_ = c.Close()
	}
	delete(p.clients, address)
	p.mu.Unlock()
}

// callWithTimeout performs client.Call with a timeout and one retry on error.
// method must be like "ReplicaRPC.PreAccept".
func callWithTimeout[T any](address, method string, args any, reply *T) error {
	startTime := time.Now()

	type rpcResult struct {
		err error
	}

	doCall := func() error {
		client, err := pool.get(address)
		if err != nil {
			return err
		}
		done := make(chan rpcResult, 1)
		go func() {
			err := client.Call(method, args, reply)
			done <- rpcResult{err: err}
		}()

		select {
		case r := <-done:
			LogRPCComplete(ReplicaID(0), address, method, time.Since(startTime), r.err == nil, r.err) // ReplicaID not critical for transport log
			return r.err
		case <-time.After(5 * time.Second):
			timeoutErr := fmt.Errorf("RPC call to %s timed out", address)
			LogRPCComplete(ReplicaID(0), address, method, time.Since(startTime), false, timeoutErr)
			return timeoutErr
		}
	}

	// First attempt
	if err := doCall(); err != nil {
		// Invalidate and retry once
		pool.invalidate(address)
		// Second attempt (fresh connection)
		return doCall()
	}
	return nil
}

// ============================================================================
//                         REPLICA-TO-REPLICA SENDERS
// ============================================================================

func SendPreAcceptToPeer(address string, args PreAcceptArgs) (*PreAcceptReply, error) {
	var reply PreAcceptReply
	if err := callWithTimeout(address, "ReplicaRPC.PreAccept", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func SendAcceptToPeer(address string, args AcceptArgs) (*AcceptReply, error) {
	var reply AcceptReply
	if err := callWithTimeout(address, "ReplicaRPC.Accept", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func SendCommitToPeer(address string, args CommitArgs) (*CommitReply, error) {
	var reply CommitReply
	if err := callWithTimeout(address, "ReplicaRPC.Commit", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}

func SendPrepareToPeer(address string, args PrepareArgs) (*PrepareReply, error) {
	var reply PrepareReply
	if err := callWithTimeout(address, "ReplicaRPC.Prepare", args, &reply); err != nil {
		return nil, err
	}
	return &reply, nil
}
