# EPaxos: Egalitarian Paxos Implementation

## Overview

This repository contains a Go implementation of the Egalitarian Paxos (EPaxos) consensus protocol. EPaxos is a leaderless, decentralized consensus algorithm that allows for high performance in geo-distributed systems. Unlike traditional Paxos or Raft, EPaxos does not rely on a single leader, which eliminates the performance bottleneck and vulnerability associated with leader-based protocols.

The implementation includes a complete distributed key-value store built on top of EPaxos, demonstrating how the consensus protocol can be used to build fault-tolerant distributed systems.

## Features

- **Leaderless Consensus**: Any replica can propose commands directly, eliminating leader bottlenecks
- **Fast Path Execution**: Commands can be committed in one round-trip when there are no conflicts
- **Conflict Detection**: Automatic detection and resolution of conflicting operations
- **Dependency Tracking**: Commands that don't conflict can execute in parallel
- **Fault Tolerance**: System continues to operate despite node failures (up to f nodes in a 2f+1 system)
- **Recovery Protocol**: Ensures consistency when replicas recover from failures
- **Key-Value Store**: Built-in distributed key-value store application

## Installation

### Prerequisites

- Go 1.15 or higher

### Building from Source

```bash
# Clone the repository
git clone https://github.com/yourusername/epaxos.git
cd epaxos

# Build the binary
go build -o epaxos
```

## Architecture

The EPaxos implementation consists of several key components:

### Core Components

1. **Replica**: The main node in the EPaxos system, responsible for:
   - Processing client requests
   - Participating in consensus
   - Executing committed commands
   - Managing the local key-value store

2. **EPaxos Protocol**: Implements the consensus algorithm with:
   - PreAccept phase
   - Accept phase (slow path)
   - Commit phase
   - Execution phase
   - Recovery protocol

3. **Key-Value Store**: A simple in-memory storage system that:
   - Supports PUT and GET operations
   - Provides thread-safe access to data
   - Serves as the application layer on top of consensus

4. **RPC System**: Handles communication between replicas using Go's RPC framework

5. **Logging System**: Comprehensive logging for debugging and monitoring

### File Structure

- `main.go`: Entry point, command-line interface, and REPL
- `epaxos.go`: Core EPaxos protocol implementation
- `replica.go`: Replica implementation and execution logic
- `command.go`: Command and instance data structures
- `rpc.go`: RPC server and client implementation
- `kvstore.go`: Key-value store implementation
- `types.go`: Common data types and structures
- `util.go`: Utility functions for conflict detection and dependency management
- `logger.go`: Logging infrastructure
- `logutil.go`: Logging utility functions
- `test.sh`: Automated test script

## How EPaxos Works

EPaxos is a consensus protocol that allows replicas to agree on an order of execution for commands. The key insight of EPaxos is that only conflicting commands need to be ordered with respect to each other, allowing for better parallelism.

### Key Concepts

1. **Commands and Instances**:
   - Each client request is encapsulated as a Command (PUT or GET)
   - Each command is assigned to an Instance (identified by ReplicaID and InstanceID)
   - Instances track the command, its dependencies, and execution status

2. **Conflict Detection**:
   - Commands conflict if they access the same key and at least one is a write
   - Conflicting commands must be ordered with respect to each other
   - Non-conflicting commands can execute in parallel

3. **Dependencies**:
   - When conflicts are detected, dependencies are created between instances
   - Dependencies form a directed graph that determines execution order
   - Cycles in the dependency graph are resolved using sequence numbers

4. **Fast and Slow Path**:
   - Fast path: If a quorum of replicas agree on sequence number and dependencies, commit immediately
   - Slow path: If there's disagreement, an additional Accept phase is required

### Protocol Phases

1. **PreAccept Phase**:
   - Leader proposes a command with initial sequence number and dependencies
   - Replicas check for conflicts and may update sequence number and dependencies
   - Replicas respond with potentially modified attributes

2. **Accept Phase (Slow Path)**:
   - If fast path conditions aren't met, leader sends Accept with merged attributes
   - Replicas acknowledge the Accept message

3. **Commit Phase**:
   - Leader notifies all replicas of the final committed values
   - Replicas mark the instance as committed

4. **Execution**:
   - Replicas build a dependency graph for each committed instance
   - Strongly connected components are identified using Tarjan's algorithm
   - Commands are executed in sequence number order within each component

5. **Recovery**:
   - If an instance is missing, replicas can initiate recovery
   - The Prepare phase gathers information about the instance
   - Based on responses, the instance is either committed or goes through Accept

### Fast Path Optimization

EPaxos uses a fast path optimization that allows commands to be committed in just one round-trip when there are no conflicts. The conditions for taking the fast path are:

1. A sufficient number of replicas (F + ⌈(F+1)/2⌉) must respond to PreAccept
2. All responses must have the same sequence number and dependencies
3. The attributes must be unchanged from the leader's proposal

If these conditions are met, the command can be committed immediately without going through the Accept phase.

### Execution Algorithm

The execution algorithm in EPaxos ensures that commands are executed in a consistent order across all replicas:

1. Build a dependency graph for the command and its dependencies
2. Find strongly connected components (SCCs) in the graph using Tarjan's algorithm
3. Sort SCCs in topological order
4. Within each SCC, execute commands in sequence number order

This approach allows non-conflicting commands to execute in parallel while ensuring that conflicting commands are executed in a consistent order.

## Usage

### Starting a Replica

```bash
./epaxos -id=0 -peersFile=peers.txt -log-level=INFO
```

### Configuration Options

- `-id`: Replica ID (must match a line in peers.txt)
- `-peersFile`: Path to the peers configuration file
- `-log-level`: Logging level (DEBUG, INFO, WARN, ERROR, FATAL)
- `-log-dir`: Directory for log files

### Peers File Format

The peers.txt file defines the replicas in the system:

```
0 localhost 8000
1 localhost 8001
2 localhost 8002
```

Each line contains:
1. Replica ID
2. Hostname
3. Port

### Interactive Commands

Once a replica is running, you can interact with it using the following commands:

- `put <key> <value>`: Store a value
- `get <key>`: Retrieve a value
- `status`: Show replica status
- `help`: Show available commands
- `exit` or `quit`: Exit the program

## Testing

The repository includes a test script (`test.sh`) that sets up a local cluster of EPaxos replicas and runs a series of commands to test the protocol:

```bash
./test.sh
```

The test script:
1. Starts three replicas
2. Sends commands to each replica, including conflicting commands
3. Analyzes the logs to verify that both fast and slow paths are working
4. Reports statistics on fast path decisions, slow path decisions, and conflicts

## Logging

The implementation includes a comprehensive logging system that records detailed information about the operation of the protocol. Log files are stored in the `logs` directory by default.

Log categories include:
- CONSENSUS: General consensus protocol events
- REPLICA: Replica lifecycle events
- EXECUTION: Command execution events
- NETWORK: Network communication events
- RPC: RPC call details
- STORAGE: Key-value store operations
- CLIENT: Client request events
- PREACCEPT: PreAccept phase events
- ACCEPT: Accept phase events
- COMMIT: Commit phase events
- DEPENDENCY: Dependency management events

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.

## Acknowledgments

This implementation is based on the EPaxos paper:
- "There Is More Consensus in Egalitarian Parliaments" by Iulian Moraru, David G. Andersen, and Michael Kaminsky (SOSP '13)
