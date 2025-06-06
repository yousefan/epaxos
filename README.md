# EPaxos - Egalitarian Paxos Implementation

## Overview

This project is an implementation of the Egalitarian Paxos (EPaxos) consensus protocol in Go. EPaxos is a leaderless, efficient consensus algorithm that allows distributed systems to agree on the order of operations. Unlike traditional Paxos or Raft, EPaxos is fully decentralized, allowing any replica to propose commands, and only orders commands that actually conflict with each other.

Key features of this implementation:

- **Leaderless Architecture**: Any replica can propose commands, eliminating the leader bottleneck
- **Conflict-Aware Ordering**: Only conflicting commands need to be ordered relative to each other
- **Fast Path Optimization**: One round-trip when no conflicts are detected
- **Slow Path Fallback**: Two round-trips when conflicts are detected
- **Dependency Tracking**: Commands can execute once their dependencies are satisfied
- **In-Memory Key-Value Store**: Simple application layer to demonstrate the consensus protocol

## Installation

### Prerequisites

- Go 1.24 or later

### Steps

1. Clone the repository:
   ```
   git clone https://github.com/yourusername/epaxos.git
   cd epaxos
   ```

2. Build the project:
   ```
   go build
   ```

## Usage

### Configuration

The system uses a `peers.txt` file to configure the replicas in the cluster. Each line in the file represents a replica with the following format:

```
<replica_id> <hostname> <port>
```

Example `peers.txt`:
```
0 localhost 8000
1 localhost 8001
2 localhost 8002
```

### Running a Replica

To start a replica, run the compiled binary with the appropriate flags:

```
./epaxos -id=<replica_id> -peersFile=<path_to_peers_file> -log-level=<log_level> -log-dir=<log_directory>
```

Parameters:
- `-id`: Replica ID (must match a line in peers.txt)
- `-peersFile`: Path to the peers.txt file (default: "peers.txt")
- `-log-level`: Logging level (DEBUG, INFO, WARN, ERROR, FATAL) (default: "INFO")
- `-log-dir`: Directory for log files (default: "logs")

Example:
```
./epaxos -id=0 -log-level=DEBUG
```

### Client Commands

Once a replica is running, you can interact with it using the built-in REPL (Read-Eval-Print Loop). The following commands are supported:

- `put <key> <value>`: Store a value in the key-value store
- `get <key>`: Retrieve a value from the key-value store

Example:
```
>> put mykey myvalue
OK
>> get mykey
Value: myvalue
```

## Architecture

### Core Components

#### Replica

The `Replica` struct is the central component of the system. It:
- Manages EPaxos instances
- Handles command proposals
- Coordinates with other replicas
- Executes committed commands
- Maintains the key-value store

#### EPaxosInstance

Each `EPaxosInstance` represents a single consensus decision. It tracks:
- The command being proposed
- Sequence number and dependencies
- Current status (PreAccepted, Accepted, Committed, Executed)
- Execution state

#### KVStore

The `KVStore` is a simple in-memory key-value store that serves as the application layer. It provides:
- `Put` and `Get` operations
- Thread-safe access to the data
- Command application logic

#### ReplicaRPC

The `ReplicaRPC` component handles network communication between replicas, exposing methods for:
- PreAccept phase
- Accept phase
- Commit phase
- Client command handling

### Protocol Phases

#### 1. PreAccept Phase

When a replica receives a command, it:
1. Creates a new EPaxos instance
2. Assigns an initial sequence number and empty dependencies
3. Sends PreAccept messages to peers
4. Peers check for conflicts and may update sequence numbers and dependencies
5. Replica collects responses

#### 2. Fast Path (No Conflicts)

If all replicas agree on the sequence number and dependencies:
1. The command is directly committed
2. Commit messages are sent to peers
3. The command is ready for execution when dependencies are satisfied

#### 3. Slow Path (Conflicts Detected)

If conflicts are detected:
1. The replica merges all dependencies
2. Sends Accept messages with the updated information
3. Collects responses
4. If a quorum is achieved, commits the command
5. Sends Commit messages to peers

#### 4. Execution

Commands are executed when:
1. They are committed
2. All their dependencies have been executed
3. The execution is applied to the key-value store

## Module Details

### main.go

The entry point of the application that:
- Parses command-line arguments
- Initializes the logger
- Reads the peers configuration
- Creates and starts a replica
- Starts the RPC server
- Provides a REPL for client interaction

### replica.go

Defines the `Replica` struct and its methods:
- `NewReplica`: Creates a new replica
- `TryExecute`: Attempts to execute a committed command

### epaxos.go

Implements the core EPaxos protocol:
- PreAccept, Accept, and Commit phases
- Fast and slow paths
- Conflict detection and resolution
- Dependency tracking

### command.go

Defines the `EPaxosInstance` struct that represents a single consensus instance.

### types.go

Contains core data structures:
- `Command`: Represents a client operation (Get/Put)
- `CommandID`: Uniquely identifies a command
- `InstanceStatus`: Tracks the state of an EPaxos instance
- `ReplicaID`: Type alias for replica identification

### kvstore.go

Implements the in-memory key-value store:
- `KVStore`: Thread-safe map with mutex protection
- `Put`: Stores a value for a key
- `Get`: Retrieves a value for a key
- `ApplyCommand`: Applies a command to the store

### rpc.go

Handles network communication:
- `ReplicaRPC`: RPC handler for the replica
- `StartRPCServer`: Initializes the RPC server
- `SendClientCommand`: Utility for sending commands to replicas

### logger.go

Provides a comprehensive logging system:
- Multiple log levels (DEBUG, INFO, WARN, ERROR, FATAL)
- Log categories for different components
- File and console output options
- Structured logging with timestamps and source information

### logutil.go

Contains utility functions for logging specific events in the EPaxos protocol:
- Phase transitions
- Conflict detection
- Dependency tracking
- Execution status

### util.go

Provides utility functions:
- `commandsConflict`: Determines if two commands conflict
- `appendIfMissing`: Adds a value to a slice if not present
- `equalIntSlice`: Compares two integer slices
- `mergeDeps`: Merges dependencies from multiple sources

## Configuration Options

### Logging

The logging system can be configured with:
- Log level: Controls verbosity (DEBUG, INFO, WARN, ERROR, FATAL)
- Log directory: Where log files are stored
- Console output: Whether to print logs to the console
- File output: Whether to write logs to a file

### Replica Setup

Replicas can be configured with:
- Replica ID: Unique identifier for the replica
- Peers file: Configuration of all replicas in the cluster

## Performance Considerations

- **Fast Path**: In the absence of conflicts, EPaxos can commit commands in just one round-trip, making it faster than traditional consensus protocols in geo-distributed settings.
- **Conflict Awareness**: By only ordering conflicting commands, EPaxos can achieve higher throughput than protocols that impose a total order on all commands.
- **Leaderless Design**: The absence of a leader eliminates the bottleneck and single point of failure present in leader-based protocols.

## Limitations

- **In-Memory Storage**: The current implementation uses in-memory storage and does not persist data to disk.
- **Simple Conflict Detection**: Conflict detection is currently limited to PUT operations on the same key.
- **No Recovery Mechanism**: The implementation does not include a recovery mechanism for replica failures.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details.
