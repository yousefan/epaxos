package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
	"time"
)

// === RPC Argument and Reply Types ===

// RPCClientRequest represents a client request over RPC
type RPCClientRequest struct {
	Command Command
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

func (r *ReplicaRPC) HandleClientCommand(req RPCClientRequest, reply *ClientReply) error {
	// Create a unique command ID for this request
	cmdID := CommandID{
		ClientID: "rpc_client",
		SeqNum:   time.Now().Nanosecond(),
	}

	// Check for duplicate request
	clientReq := &ClientRequest{
		CommandID: cmdID,
		Command:   req.Command,
		Timestamp: time.Now(),
	}

	if !r.Replica.TrackClientRequest(clientReq) {
		// Duplicate request - return cached result if available
		if _, exists := r.Replica.GetClientRequest(cmdID); exists {
			// TODO: Return cached result
			reply.Success = true
			reply.Error = "Duplicate request"
			return nil
		}
	}

	// Propose the command through EPaxos
	err := r.Replica.Propose(req.Command, cmdID)
	if err != nil {
		reply.Success = false
		reply.Error = fmt.Sprintf("Failed to propose command: %v", err)
		return nil
	}

	// For GET commands, try to retrieve the value
	if req.Command.Type == CmdGet {
		val, ok := r.Replica.KVStore.Get(req.Command.Key)
		if !ok {
			reply.Success = false
			reply.Error = "Key not found"
		} else {
			reply.Success = true
			reply.Value = val
		}
	} else {
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

// === Client Call Utility ===

func SendClientCommand(address string, cmd Command) (*ClientReply, error) {
	client, err := rpc.Dial("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to replica: %w", err)
	}
	defer client.Close()

	req := RPCClientRequest{Command: cmd}
	var reply ClientReply
	err = client.Call("ReplicaRPC.HandleClientCommand", req, &reply)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}
	return &reply, nil
}
