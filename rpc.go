package main

import (
	"fmt"
	"log"
	"net"
	"net/rpc"
)

// === RPC Argument and Reply Types ===

// Put/Get request from clients
type ClientRequest struct {
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

func (r *ReplicaRPC) HandleClientCommand(req ClientRequest, reply *ClientReply) error {
	switch req.Command.Type {
	case CmdPut:
		r.Replica.KVStore.Put(req.Command.Key, req.Command.Value)
		reply.Success = true

	case CmdGet:
		val, ok := r.Replica.KVStore.Get(req.Command.Key)
		if !ok {
			reply.Success = false
			reply.Error = "Key not found"
		} else {
			reply.Success = true
			reply.Value = val
		}

	default:
		reply.Success = false
		reply.Error = "Unknown command type"
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

	req := ClientRequest{Command: cmd}
	var reply ClientReply
	err = client.Call("ReplicaRPC.HandleClientCommand", req, &reply)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}
	return &reply, nil
}
