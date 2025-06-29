package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	// Parse startup flags
	id := flag.Int("id", 0, "Replica ID (must match line in peers.txt)")
	peersFile := flag.String("peersFile", "peers.txt", "Path to peers.txt file")
	logLevel := flag.String("log-level", "INFO", "Log level (DEBUG, INFO, WARN, ERROR, FATAL)")
	logDir := flag.String("log-dir", "logs", "Directory for log files")
	flag.Parse()

	// Initialize logger
	var level LogLevel
	switch strings.ToUpper(*logLevel) {
	case "DEBUG":
		level = DEBUG
	case "INFO":
		level = INFO
	case "WARN":
		level = WARN
	case "ERROR":
		level = ERROR
	case "FATAL":
		level = FATAL
	default:
		level = INFO
	}

	logConfig := LoggerConfig{
		Level:         level,
		ReplicaID:     ReplicaID(*id),
		LogDir:        *logDir,
		LogFileName:   fmt.Sprintf("epaxos_replica_%d.log", *id),
		ConsoleOutput: false, // Disable console output to avoid interference with REPL
		FileOutput:    true,
	}

	if err := InitLogger(logConfig); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer GetLogger().Close()

	// Log startup
	GetLogger().Info(GENERAL, "EPaxos replica %d starting...", *id)

	// Open and parse peers.txt
	file, err := os.Open(*peersFile)
	if err != nil {
		GetLogger().Fatal(GENERAL, "Failed to open peers file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var (
		thisAddr string
		peers    []string
	)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			GetLogger().Fatal(GENERAL, "Invalid line in peers.txt: %s", line)
		}

		lineID := parts[0]
		host := parts[1]
		port := parts[2]

		if lineID == fmt.Sprint(*id) {
			thisAddr = fmt.Sprintf("%s:%s", host, port)
		} else {
			peers = append(peers, fmt.Sprintf("%s:%s", host, port))
		}
	}

	if thisAddr == "" {
		GetLogger().Fatal(GENERAL, "Could not find self ID (%d) in peers.txt", *id)
	}

	// Initialize the replica
	replica := NewReplica(ReplicaID(*id), peers)
	LogReplicaStart(replica.ID, thisAddr, peers)

	// Start RPC server
	if err := StartRPCServer(replica, thisAddr); err != nil {
		GetLogger().Fatal(NETWORK, "Failed to start RPC server: %v", err)
	}

	// Improved background execution loop with better error handling
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// Collect instances to execute
			replica.InstanceLock.RLock()
			var instancesToExecute []struct{ rid, iid int }

			for rid, instMap := range replica.Instances {
				for iid, inst := range instMap {
					if inst != nil && inst.Committed && !inst.Executed {
						instancesToExecute = append(instancesToExecute, struct{ rid, iid int }{rid, iid})
					}
				}
			}
			replica.InstanceLock.RUnlock()

			// Execute instances with limited concurrency to avoid resource exhaustion
			const maxConcurrentExecutions = 10
			semaphore := make(chan struct{}, maxConcurrentExecutions)

			for _, inst := range instancesToExecute {
				semaphore <- struct{}{} // Acquire
				go func(rid, iid int) {
					defer func() { <-semaphore }() // Release
					defer func() {
						if r := recover(); r != nil {
							GetLogger().Error(EXECUTION, "Panic in TryExecute for R%d.%d: %v", rid, iid, r)
						}
					}()
					replica.TryExecute(rid, iid)
				}(inst.rid, inst.iid)
			}
		}
	}()

	// REPL to simulate client commands
	reader := bufio.NewReader(os.Stdin)
	GetLogger().Info(CLIENT, "Replica is running. Type 'put key value' or 'get key':")
	fmt.Println("Replica is running. Type 'put key value' or 'get key':")
	fmt.Println("Commands: put <key> <value>, get <key>, status, exit")

	for {
		fmt.Print(">> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		args := strings.Split(input, " ")
		if len(args) < 1 {
			fmt.Println("Invalid command")
			continue
		}

		switch args[0] {
		case "put":
			if len(args) != 3 {
				fmt.Println("Usage: put <key> <value>")
				continue
			}
			cmd := Command{
				Type:  CmdPut,
				Key:   args[1],
				Value: args[2],
			}
			cmdID := CommandID{ClientID: "cli", SeqNum: time.Now().Nanosecond()}
			LogClientRequest(replica.ID, cmd, cmdID)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Error(CLIENT, "Error executing PUT: %v", err)
				fmt.Printf("Error: %v\n", err)
			} else {
				fmt.Printf("OK (took %v)\n", duration)
			}

		case "get":
			if len(args) != 2 {
				fmt.Println("Usage: get <key>")
				continue
			}
			cmd := Command{
				Type: CmdGet,
				Key:  args[1],
			}
			cmdID := CommandID{ClientID: "cli", SeqNum: time.Now().Nanosecond()}
			LogClientRequest(replica.ID, cmd, cmdID)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Error(CLIENT, "Error executing GET: %v", err)
				fmt.Printf("Error: %v\n", err)
			} else {
				// Wait a moment for execution to complete, then read the result
				time.Sleep(100 * time.Millisecond)
				val, ok := replica.KVStore.Get(cmd.Key)
				if !ok {
					fmt.Printf("Value: <not found> (took %v)\n", duration)
				} else {
					fmt.Printf("Value: %s (took %v)\n", val, duration)
				}
			}

		case "status":
			// Show replica status
			replica.InstanceLock.RLock()
			totalInstances := 0
			committedInstances := 0
			executedInstances := 0

			for _, instMap := range replica.Instances {
				for _, inst := range instMap {
					if inst != nil {
						totalInstances++
						if inst.Committed {
							committedInstances++
						}
						if inst.Executed {
							executedInstances++
						}
					}
				}
			}
			replica.InstanceLock.RUnlock()

			fmt.Printf("Replica %d Status:\n", replica.ID)
			fmt.Printf("  Total instances: %d\n", totalInstances)
			fmt.Printf("  Committed instances: %d\n", committedInstances)
			fmt.Printf("  Executed instances: %d\n", executedInstances)
			fmt.Printf("  KV Store size: %d keys\n", replica.KVStore.Size())
			fmt.Printf("  Next instance ID: %d\n", replica.NextInstance)

		case "exit", "quit":
			GetLogger().Info(GENERAL, "Replica %d shutting down", *id)
			fmt.Println("Goodbye!")
			return

		case "help":
			fmt.Println("Available commands:")
			fmt.Println("  put <key> <value> - Store a value")
			fmt.Println("  get <key>         - Retrieve a value")
			fmt.Println("  status            - Show replica status")
			fmt.Println("  help              - Show this help")
			fmt.Println("  exit/quit         - Exit the program")

		default:
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", args[0])
		}
	}
}
