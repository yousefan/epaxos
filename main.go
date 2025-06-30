package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
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

	// Start HTTP server instead of REPL
	go func() {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			response := map[string]interface{}{
				"replica_id": replica.ID,
				"address":    thisAddr,
				"peers":      peers,
				"status":     "running",
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		})

		http.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			key := r.URL.Query().Get("key")
			if key == "" {
				http.Error(w, "Missing key parameter", http.StatusBadRequest)
				return
			}

			cmd := Command{
				Type: CmdGet,
				Key:  key,
			}
			cmdID := CommandID{ClientID: "http", SeqNum: time.Now().Nanosecond()}
			LogClientRequest(replica.ID, cmd, cmdID)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Error(CLIENT, "Error executing GET: %v", err)
				http.Error(w, fmt.Sprintf("Error: %v", err), http.StatusInternalServerError)
				return
			}

			// Wait a moment for execution to complete, then read the result
			time.Sleep(100 * time.Millisecond)
			val, ok := replica.KVStore.Get(cmd.Key)

			response := map[string]interface{}{
				"key":      key,
				"found":    ok,
				"duration": duration.String(),
			}

			if ok {
				response["value"] = val
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		})

		http.HandleFunc("/put", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
				return
			}

			key := r.FormValue("key")
			value := r.FormValue("value")

			if key == "" || value == "" {
				http.Error(w, "Missing key or value parameter", http.StatusBadRequest)
				return
			}

			cmd := Command{
				Type:  CmdPut,
				Key:   key,
				Value: value,
			}
			cmdID := CommandID{ClientID: "http", SeqNum: time.Now().Nanosecond()}
			LogClientRequest(replica.ID, cmd, cmdID)

			start := time.Now()
			err := replica.Propose(cmd, cmdID)
			duration := time.Since(start)

			if err != nil {
				GetLogger().Error(CLIENT, "Error executing PUT: %v", err)
				http.Error(w, fmt.Sprintf("Error: %v", err), http.StatusInternalServerError)
				return
			}

			response := map[string]interface{}{
				"key":      key,
				"value":    value,
				"status":   "success",
				"duration": duration.String(),
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		})

		// Extract port from thisAddr for HTTP server
		parts := strings.Split(thisAddr, ":")
		httpPort := "8080" // default
		if len(parts) == 2 {
			// Use RPC port + 1000 for HTTP server
			rpcPort := parts[1]
			if port, err := strconv.Atoi(rpcPort); err == nil {
				httpPort = strconv.Itoa(port + 1000)
			}
		}

		httpAddr := fmt.Sprintf(":%s", httpPort)
		GetLogger().Info(CLIENT, "Starting HTTP server on %s", httpAddr)
		fmt.Printf("HTTP server starting on %s\n", httpAddr)

		if err := http.ListenAndServe(httpAddr, nil); err != nil {
			GetLogger().Fatal(NETWORK, "Failed to start HTTP server: %v", err)
		}
	}()

	// Keep the main goroutine alive
	GetLogger().Info(GENERAL, "Replica %d is running with HTTP server", *id)
	fmt.Printf("Replica %d is running. HTTP endpoints:\n", *id)
	fmt.Printf("  GET  / - Server info\n")
	fmt.Printf("  GET  /get?key=<key> - Get value\n")
	fmt.Printf("  POST /put - Put key/value (form data)\n")
	fmt.Println("Press Ctrl+C to exit")

	// Block forever (or until interrupted)
	select {}
}
