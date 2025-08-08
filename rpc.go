package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
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

// === Client Call Utility ===

func SendClientCommand(address string, cmd Command, commandCount int) (*ClientReply, error) {
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
