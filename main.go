// Updated main.go with enhanced structured logging configuration
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
	// Parse startup flags with additional logging options
	id := flag.Int("id", 0, "Replica ID (must match line in peers.txt)")
	peersFile := flag.String("peersFile", "peers.txt", "Path to peers.txt file")
	logLevel := flag.String("log-level", "INFO", "Log level (DEBUG, INFO, WARN, ERROR, FATAL)")
	logDir := flag.String("log-dir", "logs", "Directory for log files")
	jsonLogs := flag.Bool("json-logs", true, "Enable structured JSON logging")
	consoleLogs := flag.Bool("console-logs", false, "Enable console output (disable for production)")
	flag.Parse()

	// Initialize enhanced structured logger
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
		ConsoleOutput: *consoleLogs,
		FileOutput:    true,
		JSONOutput:    *jsonLogs,
	}

	if err := InitLogger(logConfig); err != nil {
		fmt.Printf("Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer GetLogger().Close()

	// Enhanced startup logging with comprehensive context
	startupStart := time.Now()

	// Open and parse peers.txt with enhanced error handling and logging
	file, err := os.Open(*peersFile)
	if err != nil {
		GetLogger().Log(FATAL, GENERAL, "Failed to open peers file").
			WithError(err, "file_error").
			WithContext("peers_file", *peersFile).
			WithTags("startup", "error", "peers").
			Send()
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var (
		thisAddr string
		peers    []string
		lineNum  int
	)

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			GetLogger().Log(FATAL, GENERAL, "Invalid line in peers.txt").
				WithContext("line_number", lineNum).
				WithContext("line_content", line).
				WithContext("expected_fields", 3).
				WithContext("actual_fields", len(parts)).
				WithTags("startup", "error", "peers", "parsing").
				Send()
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
		GetLogger().Log(FATAL, GENERAL, "Could not find self ID in peers.txt").
			WithContext("self_id", *id).
			WithContext("lines_processed", lineNum).
			WithTags("startup", "error", "peers", "self_not_found").
			Send()
	}

	// Initialize the replica with startup timing
	replica := NewReplica(ReplicaID(*id), peers)

	//LogReplicaStart(replica.ID, thisAddr, peers)

	if err := StartRPCServer(replica, thisAddr); err != nil {
		GetLogger().Log(FATAL, NETWORK, "Failed to start RPC server").
			WithError(err, "rpc_server_error").
			WithContext("address", thisAddr).
			WithTags("startup", "error", "rpc").
			Send()
	}

	const execWorkers = 16
	for i := 0; i < execWorkers; i++ {
		go func() {
			for k := range replica.readyCh {
				// Attempt execution
				executed := replica.TryExecute(k.rid, k.iid)
				// Clean up pending flag regardless; re-enqueue only via dependency notifications
				replica.InstanceLock.Lock()
				delete(replica.pending, makeKey(k.rid, k.iid))
				replica.InstanceLock.Unlock()

				if executed {
					replica.onExecuted(k.rid, k.iid)
				} else {
					// Still blocked? We'll wake it via onExecuted(dep) later.
					// Optional: set a small fallback timer to retry in case of missed signals.
				}
			}
		}()
	}

	// Enhanced REPL with comprehensive command logging
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("EPaxos Replica is running. Available commands:")
	fmt.Println("  put <key> <value> - Store a value")
	fmt.Println("  get <key>         - Retrieve a value")
	fmt.Println("  status            - Show replica status")
	fmt.Println("  help              - Show this help")
	fmt.Println("  exit/quit         - Exit the program")

	commandCount := 0

	for {
		fmt.Print(">> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			GetLogger().Log(ERROR, CLIENT, "Error reading REPL input").
				WithError(err, "input_error").
				WithTags("repl", "error", "input").
				Send()
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		commandCount++
		commandStart := time.Now()
		args := strings.Split(input, " ")

		if len(args) < 1 {
			fmt.Println("Invalid command")
			continue
		}

		switch args[0] {
		case "put":
			if len(args) != 3 {
				fmt.Println("Usage: put <key> <value>")
				GetLogger().Log(WARN, CLIENT, "Invalid PUT command syntax").
					WithContext("args_provided", len(args)).
					WithContext("command_count", commandCount).
					WithTags("repl", "error", "syntax").
					Send()
				continue
			}

			cmd := Command{
				Type:  CmdPut,
				Key:   args[1],
				Value: args[2],
			}
			cmdID := CommandID{ClientID: "cli", SeqNum: time.Now().Nanosecond()}

			LogClientRequest(replica.ID, cmd, cmdID, commandCount)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Log(ERROR, CLIENT, "PUT command failed").
					WithCommand(cmd, cmdID).
					WithError(err, "proposal_error").
					WithDuration(duration).
					WithContext("command_count", commandCount).
					WithTags("repl", "put", "failed").
					Send()
				fmt.Printf("Error: %v\n", err)
			} else {
				GetLogger().Log(INFO, CLIENT, "PUT command completed").
					WithCommand(cmd, cmdID).
					WithDuration(duration).
					WithContext("command_count", commandCount).
					WithTags("repl", "put", "success").
					Send()
				fmt.Printf("OK (took %v)\n", duration)
			}

		case "get":
			if len(args) != 2 {
				fmt.Println("Usage: get <key>")
				GetLogger().Log(WARN, CLIENT, "Invalid GET command syntax").
					WithContext("args_provided", len(args)).
					WithContext("command_count", commandCount).
					WithTags("repl", "error", "syntax").
					Send()
				continue
			}

			cmd := Command{
				Type: CmdGet,
				Key:  args[1],
			}
			cmdID := CommandID{ClientID: "cli", SeqNum: time.Now().Nanosecond()}

			LogClientRequest(replica.ID, cmd, cmdID, commandCount)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Log(ERROR, CLIENT, "GET command failed").
					WithCommand(cmd, cmdID).
					WithError(err, "proposal_error").
					WithDuration(duration).
					WithContext("command_count", commandCount).
					WithTags("repl", "get", "failed").
					Send()
				fmt.Printf("Error: %v\n", err)
			} else {
				// Wait a moment for execution to complete, then read the result
				time.Sleep(100 * time.Millisecond)
				val, ok := replica.KVStore.Get(cmd.Key)

				GetLogger().Log(INFO, CLIENT, "GET command completed").
					WithCommand(cmd, cmdID).
					WithDuration(duration).
					WithContext("command_count", commandCount).
					WithContext("value_found", ok).
					WithContext("value", val).
					WithTags("repl", "get", "success").
					Send()

				if !ok {
					fmt.Printf("Value: <not found> (took %v)\n", duration)
				} else {
					fmt.Printf("Value: %s (took %v)\n", val, duration)
				}
			}

		case "status":
			statusStart := time.Now()
			// Show enhanced replica status with comprehensive metrics
			replica.InstanceLock.RLock()
			totalInstances := 0
			committedInstances := 0
			executedInstances := 0
			preAcceptedInstances := 0
			acceptedInstances := 0
			instancesByReplica := make(map[int]int)

			for rid, instMap := range replica.Instances {
				instancesByReplica[rid] = len(instMap)
				for _, inst := range instMap {
					if inst != nil {
						totalInstances++
						switch inst.Status {
						case StatusPreAccepted:
							preAcceptedInstances++
						case StatusAccepted:
							acceptedInstances++
						case StatusCommitted:
							committedInstances++
						case StatusExecuted:
							executedInstances++
						}
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

			kvStoreSize := replica.KVStore.Size()
			statusDuration := time.Since(statusStart)

			GetLogger().Log(INFO, GENERAL, "Status command executed").
				WithContext("command_count", commandCount).
				WithContext("total_instances", totalInstances).
				WithContext("committed_instances", committedInstances).
				WithContext("executed_instances", executedInstances).
				WithContext("preaccepted_instances", preAcceptedInstances).
				WithContext("accepted_instances", acceptedInstances).
				WithContext("kv_store_size", kvStoreSize).
				WithContext("next_instance_id", replica.NextInstance).
				WithContext("instances_by_replica", instancesByReplica).
				WithContext("status_query_duration_ms", statusDuration.Milliseconds()).
				WithTags("repl", "status", "query").
				Send()

			fmt.Printf("Replica %d Status:\n", replica.ID)
			fmt.Printf("  Total instances: %d\n", totalInstances)
			fmt.Printf("  Pre-accepted instances: %d\n", preAcceptedInstances)
			fmt.Printf("  Accepted instances: %d\n", acceptedInstances)
			fmt.Printf("  Committed instances: %d\n", committedInstances)
			fmt.Printf("  Executed instances: %d\n", executedInstances)
			fmt.Printf("  KV Store size: %d keys\n", kvStoreSize)
			fmt.Printf("  Next instance ID: %d\n", replica.NextInstance)
			fmt.Printf("  Instances by replica: %v\n", instancesByReplica)
			fmt.Printf("  Status query took: %v\n", statusDuration)

		case "exit", "quit":
			shutdownStart := time.Now()
			GetLogger().Log(INFO, GENERAL, "Replica shutdown initiated").
				WithContext("command_count", commandCount).
				WithContext("uptime_ms", time.Since(startupStart).Milliseconds()).
				WithTags("shutdown", "initiated").
				Send()

			LogReplicaShutdown(replica.ID, "user_command")

			shutdownDuration := time.Since(shutdownStart)
			GetLogger().Log(INFO, GENERAL, "Replica shutdown completed").
				WithContext("shutdown_duration_ms", shutdownDuration.Milliseconds()).
				WithContext("total_commands_processed", commandCount).
				WithTags("shutdown", "completed").
				Send()

			fmt.Println("Goodbye!")
			return

		case "help":
			GetLogger().Log(DEBUG, CLIENT, "Help command executed").
				WithContext("command_count", commandCount).
				WithTags("repl", "help").
				Send()

			fmt.Println("Available commands:")
			fmt.Println("  put <key> <value> - Store a value")
			fmt.Println("  get <key>         - Retrieve a value")
			fmt.Println("  status            - Show replica status")
			fmt.Println("  help              - Show this help")
			fmt.Println("  exit/quit         - Exit the program")

		case "debug":
			// Hidden debug command for development
			if len(args) < 2 {
				fmt.Println("Debug usage: debug <component>")
				continue
			}

			component := args[1]
			debugStart := time.Now()

			switch component {
			case "instances":
				replica.InstanceLock.RLock()
				for rid, instMap := range replica.Instances {
					for iid, inst := range instMap {
						LogInstanceDump(ReplicaID(rid), iid, inst)
					}
				}
				replica.InstanceLock.RUnlock()
				fmt.Printf("Instance dump completed (check logs)\n")

			case "performance":
				metrics := map[string]interface{}{
					"commands_processed": commandCount,
					"uptime_ms":          time.Since(startupStart).Milliseconds(),
					"avg_command_rate":   float64(commandCount) / time.Since(startupStart).Seconds(),
				}
				LogPerformanceMetrics(replica.ID, metrics)
				fmt.Printf("Performance metrics logged\n")

			default:
				fmt.Printf("Unknown debug component: %s\n", component)
			}

			debugDuration := time.Since(debugStart)
			GetLogger().Log(DEBUG, GENERAL, "Debug command executed").
				WithContext("component", component).
				WithContext("command_count", commandCount).
				WithContext("debug_duration_ms", debugDuration.Milliseconds()).
				WithTags("repl", "debug", component).
				Send()

		default:
			GetLogger().Log(WARN, CLIENT, "Unknown REPL command").
				WithContext("unknown_command", args[0]).
				WithContext("command_count", commandCount).
				WithTags("repl", "error", "unknown").
				Send()
			fmt.Printf("Unknown command: %s. Type 'help' for available commands.\n", args[0])
		}

		commandDuration := time.Since(commandStart)
		if commandDuration > 5*time.Second {
			GetLogger().Log(WARN, CLIENT, "REPL command took longer than expected").
				WithContext("command", args[0]).
				WithContext("command_count", commandCount).
				WithContext("command_duration_ms", commandDuration.Milliseconds()).
				WithTags("repl", "performance", "slow").
				Send()
		}
	}
}
