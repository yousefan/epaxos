package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

func main() {
	// Parse startup flags
	// Replace flag parsing with this:
	id := flag.Int("id", 0, "Replica ID (must match line in peers.txt)")
	peersFile := flag.String("peersFile", "peers.txt", "Path to peers.txt file")
	flag.Parse()

	// Open and parse peers.txt
	file, err := os.Open(*peersFile)
	if err != nil {
		log.Fatalf("Failed to open peers file: %v", err)
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
			log.Fatalf("Invalid line in peers.txt: %s", line)
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
		log.Fatalf("Could not find self ID (%d) in peers.txt", *id)
	}

	// Initialize the replica
	replica := NewReplica(ReplicaID(*id), peers)

	// Start RPC server
	if err := StartRPCServer(replica, thisAddr); err != nil {
		log.Fatalf("Failed to start RPC server: %v", err)
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
	fmt.Println("Replica is running. Type 'put key value' or 'get key':")
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
			err := replica.Propose(cmd, cmdID)
			if err != nil {
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
			err := replica.Propose(cmd, cmdID)
			val, _ := replica.KVStore.Get(cmd.Key)
			if err != nil {
				fmt.Println("Error:", err)
			} else {
				fmt.Println("Value:", val)
			}

		default:
			fmt.Println("Unknown command")
		}
	}
}
