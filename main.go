// Updated main.go with logger initialization
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
		ConsoleOutput: false,
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

	go func() {
		for {
			replica.InstanceLock.RLock()
			for rid, instMap := range replica.Instances {
				for iid := range instMap {
					go replica.TryExecute(rid, iid)
				}
			}
			replica.InstanceLock.RUnlock()
			time.Sleep(1 * time.Second)
		}
	}()

	// REPL to simulate client commands
	reader := bufio.NewReader(os.Stdin)
	GetLogger().Info(CLIENT, "Replica is running. Type 'put key value' or 'get key':")
	for {
		fmt.Print(">> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" {
			continue
		}

		args := strings.Split(input, " ")
		if len(args) < 2 {
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
			err := replica.Propose(cmd, cmdID)
			if err != nil {
				GetLogger().Error(CLIENT, "Error executing PUT: %v", err)
				fmt.Println("Error:", err)
			} else {
				fmt.Println("OK")
			}

		case "get":
			cmd := Command{
				Type: CmdGet,
				Key:  args[1],
			}
			cmdID := CommandID{ClientID: "cli", SeqNum: time.Now().Nanosecond()}
			LogClientRequest(replica.ID, cmd, cmdID)
			err := replica.Propose(cmd, cmdID)
			val, _ := replica.KVStore.Get(cmd.Key)
			if err != nil {
				GetLogger().Error(CLIENT, "Error executing GET: %v", err)
				fmt.Println("Error:", err)
			} else {
				fmt.Println("Value:", val)
			}

		default:
			fmt.Println("Unknown command")
		}
	}
}
