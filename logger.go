// logger.go - Enhanced structured logging
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogLevel represents the severity of a log message
type LogLevel int

const (
	// Log levels
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
	FATAL
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	case FATAL:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// LogCategory represents the component or subsystem that generated the log
type LogCategory string

const (
	// Log categories
	CONSENSUS  LogCategory = "CONSENSUS"
	REPLICA    LogCategory = "REPLICA"
	EXECUTION  LogCategory = "EXECUTION"
	NETWORK    LogCategory = "NETWORK"
	RPC        LogCategory = "RPC"
	STORAGE    LogCategory = "STORAGE"
	CLIENT     LogCategory = "CLIENT"
	GENERAL    LogCategory = "GENERAL"
	PREACCEPT  LogCategory = "PREACCEPT"
	ACCEPT     LogCategory = "ACCEPT"
	COMMIT     LogCategory = "COMMIT"
	DEPENDENCY LogCategory = "DEPENDENCY"
	RECOVERY   LogCategory = "RECOVERY"
)

// StructuredLogEntry represents a complete log entry with all possible fields
type StructuredLogEntry struct {
	// Core fields
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Category  string    `json:"category"`
	Message   string    `json:"message"`
	ReplicaID int       `json:"replica_id"`
	//SourceFile string    `json:"source_file,omitempty"`
	//SourceLine int       `json:"source_line,omitempty"`

	// EPaxos specific fields
	InstanceID      *int         `json:"instance_id,omitempty"`
	TargetReplicaID *int         `json:"target_replica_id,omitempty"`
	CommandID       *CommandID   `json:"command_id,omitempty"`
	Command         *Command     `json:"command,omitempty"`
	Sequence        *int         `json:"sequence,omitempty"`
	Dependencies    []Dependency `json:"dependencies,omitempty"`
	Ballot          *Ballot      `json:"ballot,omitempty"`
	Status          *string      `json:"status,omitempty"`
	Phase           *string      `json:"phase,omitempty"`

	// State changes
	OldStatus       *string      `json:"old_status,omitempty"`
	NewStatus       *string      `json:"new_status,omitempty"`
	OldSequence     *int         `json:"old_sequence,omitempty"`
	NewSequence     *int         `json:"new_sequence,omitempty"`
	OldDependencies []Dependency `json:"old_dependencies,omitempty"`
	NewDependencies []Dependency `json:"new_dependencies,omitempty"`

	// Consensus specific
	QuorumSize          *int  `json:"quorum_size,omitempty"`
	ReceivedResponses   *int  `json:"received_responses,omitempty"`
	UnchangedResponses  *int  `json:"unchanged_responses,omitempty"`
	FastPathEligible    *bool `json:"fast_path_eligible,omitempty"`
	AttributesUnchanged *bool `json:"attributes_unchanged,omitempty"`

	// Execution specific
	ExecutionOrder      *int `json:"execution_order,omitempty"`
	SCCSize             *int `json:"scc_size,omitempty"`
	DependencyGraphSize *int `json:"dependency_graph_size,omitempty"`

	// Network/RPC specific
	TargetAddress *string `json:"target_address,omitempty"`
	RPCMethod     *string `json:"rpc_method,omitempty"`
	RPCDuration   *int64  `json:"rpc_duration_ms,omitempty"`
	RPCSuccess    *bool   `json:"rpc_success,omitempty"`

	// KV Store specific
	Key         *string `json:"key,omitempty"`
	Value       *string `json:"value,omitempty"`
	KVOperation *string `json:"kv_operation,omitempty"`

	// Error information
	Error     *string `json:"error,omitempty"`
	ErrorCode *string `json:"error_code,omitempty"`

	// Performance metrics
	Duration  *float64   `json:"duration_ms,omitempty"`
	StartTime *time.Time `json:"start_time,omitempty"`
	EndTime   *time.Time `json:"end_time,omitempty"`

	// Recovery specific
	RecoveryReason  *string `json:"recovery_reason,omitempty"`
	RecoveryAttempt *int    `json:"recovery_attempt,omitempty"`

	// Client specific
	ClientID *string `json:"client_id,omitempty"`

	// Additional context
	Context map[string]interface{} `json:"context,omitempty"`
	Tags    []string               `json:"tags,omitempty"`
}

// EPaxosLogger is a custom structured logger for the EPaxos system
type EPaxosLogger struct {
	mu            sync.Mutex
	level         LogLevel
	consoleOutput *log.Logger
	fileOutput    *log.Logger
	replicaID     ReplicaID
	logFile       *os.File
	jsonOutput    bool
}

// LoggerConfig holds configuration for the logger
type LoggerConfig struct {
	Level         LogLevel
	ReplicaID     ReplicaID
	LogDir        string
	LogFileName   string
	ConsoleOutput bool
	FileOutput    bool
	JSONOutput    bool // Enable structured JSON logging
}

// DefaultLoggerConfig returns a default configuration for the logger
func DefaultLoggerConfig(replicaID ReplicaID) LoggerConfig {
	return LoggerConfig{
		Level:         INFO,
		ReplicaID:     replicaID,
		LogDir:        "logs",
		LogFileName:   fmt.Sprintf("epaxos_replica_%d.log", replicaID),
		ConsoleOutput: true,
		FileOutput:    true,
		JSONOutput:    true,
	}
}

// NewLogger creates a new EPaxos logger
func NewLogger(config LoggerConfig) (*EPaxosLogger, error) {
	logger := &EPaxosLogger{
		level:      config.Level,
		replicaID:  config.ReplicaID,
		jsonOutput: config.JSONOutput,
	}

	// Console output
	if config.ConsoleOutput {
		logger.consoleOutput = log.New(os.Stdout, "", 0)
	}

	// File output
	if config.FileOutput {
		// Create log directory if it doesn't exist
		if err := os.MkdirAll(config.LogDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}

		// Open log file
		logPath := filepath.Join(config.LogDir, config.LogFileName)
		file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open log file: %w", err)
		}

		logger.logFile = file
		logger.fileOutput = log.New(file, "", 0)
	}

	return logger, nil
}

// LogBuilder provides a fluent interface for building structured log entries
type LogBuilder struct {
	entry  *StructuredLogEntry
	logger *EPaxosLogger
}

// Log creates a new structured log builder
func (l *EPaxosLogger) Log(level LogLevel, category LogCategory, message string) *LogBuilder {
	if level < l.level {
		return &LogBuilder{entry: nil, logger: l} // Return inactive builder
	}

	// Get caller information
	//_, file, line, ok := runtime.Caller(1)
	//sourceFile := ""
	//sourceLine := 0
	//if ok {
	//	sourceFile = filepath.Base(file)
	//	sourceLine = line
	//}

	entry := &StructuredLogEntry{
		Timestamp: time.Now(),
		Level:     level.String(),
		Category:  string(category),
		Message:   message,
		ReplicaID: int(l.replicaID),
	}

	return &LogBuilder{entry: entry, logger: l}
}

// Fluent interface methods for LogBuilder
func (b *LogBuilder) WithInstance(replicaID, instanceID int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.TargetReplicaID = &replicaID
	b.entry.InstanceID = &instanceID
	return b
}

func (b *LogBuilder) WithCommand(cmd Command, cmdID CommandID) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Command = &cmd
	b.entry.CommandID = &cmdID
	return b
}

func (b *LogBuilder) WithSequence(seq int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Sequence = &seq
	return b
}

func (b *LogBuilder) WithDependencies(deps []Dependency) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Dependencies = deps
	return b
}

func (b *LogBuilder) WithBallot(ballot Ballot) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Ballot = &ballot
	return b
}

func (b *LogBuilder) WithStatus(status InstanceStatus) *LogBuilder {
	if b.entry == nil {
		return b
	}
	statusStr := formatStatus(status)
	b.entry.Status = &statusStr
	return b
}

func (b *LogBuilder) WithPhase(phase string) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Phase = &phase
	return b
}

func (b *LogBuilder) WithStateChange(oldStatus, newStatus InstanceStatus) *LogBuilder {
	if b.entry == nil {
		return b
	}
	oldStr := formatStatus(oldStatus)
	newStr := formatStatus(newStatus)
	b.entry.OldStatus = &oldStr
	b.entry.NewStatus = &newStr
	return b
}

func (b *LogBuilder) WithSequenceChange(oldSeq, newSeq int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.OldSequence = &oldSeq
	b.entry.NewSequence = &newSeq
	return b
}

func (b *LogBuilder) WithDependencyChange(oldDeps, newDeps []Dependency) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.OldDependencies = oldDeps
	b.entry.NewDependencies = newDeps
	return b
}

func (b *LogBuilder) WithQuorum(quorumSize, received, unchanged int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.QuorumSize = &quorumSize
	b.entry.ReceivedResponses = &received
	b.entry.UnchangedResponses = &unchanged
	return b
}

func (b *LogBuilder) WithFastPath(eligible bool) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.FastPathEligible = &eligible
	return b
}

func (b *LogBuilder) WithAttributesUnchanged(unchanged bool) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.AttributesUnchanged = &unchanged
	return b
}

func (b *LogBuilder) WithExecution(order, sccSize, graphSize int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.ExecutionOrder = &order
	b.entry.SCCSize = &sccSize
	b.entry.DependencyGraphSize = &graphSize
	return b
}

func (b *LogBuilder) WithRPC(address, method string, duration time.Duration, success bool) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.TargetAddress = &address
	b.entry.RPCMethod = &method
	durationMs := duration.Milliseconds()
	b.entry.RPCDuration = &durationMs
	b.entry.RPCSuccess = &success
	return b
}

func (b *LogBuilder) WithKV(operation, key, value string) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.KVOperation = &operation
	b.entry.Key = &key
	b.entry.Value = &value
	return b
}

func (b *LogBuilder) WithError(err error, code string) *LogBuilder {
	if b.entry == nil {
		return b
	}
	if err != nil {
		errStr := err.Error()
		b.entry.Error = &errStr
	}
	if code != "" {
		b.entry.ErrorCode = &code
	}
	return b
}

func (b *LogBuilder) WithDuration(duration time.Duration) *LogBuilder {
	if b.entry == nil {
		return b
	}
	// Convert nanoseconds to milliseconds as float64
	durationMs := float64(duration.Nanoseconds()) / 1e6
	b.entry.Duration = &durationMs
	return b
}

func (b *LogBuilder) WithTimeRange(start, end time.Time) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.StartTime = &start
	b.entry.EndTime = &end
	return b
}

func (b *LogBuilder) WithRecovery(reason string, attempt int) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.RecoveryReason = &reason
	b.entry.RecoveryAttempt = &attempt
	return b
}

func (b *LogBuilder) WithClient(clientID string) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.ClientID = &clientID
	return b
}

func (b *LogBuilder) WithContext(key string, value interface{}) *LogBuilder {
	if b.entry == nil {
		return b
	}
	if b.entry.Context == nil {
		b.entry.Context = make(map[string]interface{})
	}
	b.entry.Context[key] = value
	return b
}

func (b *LogBuilder) WithTags(tags ...string) *LogBuilder {
	if b.entry == nil {
		return b
	}
	b.entry.Tags = append(b.entry.Tags, tags...)
	return b
}

// Send finalizes and outputs the log entry
func (b *LogBuilder) Send() {
	if b.entry == nil || b.logger == nil {
		return
	}

	b.logger.writeLog(b.entry)

	if b.entry.Level == "FATAL" {
		os.Exit(1)
	}
}

// writeLog writes the structured log entry to all configured outputs
func (l *EPaxosLogger) writeLog(entry *StructuredLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var output string
	if l.jsonOutput {
		jsonBytes, err := json.Marshal(entry)
		if err != nil {
			// Fallback to simple format if JSON marshaling fails
			output = l.formatSimpleLog(entry)
		} else {
			output = string(jsonBytes)
		}
	} else {
		output = l.formatSimpleLog(entry)
	}

	if l.consoleOutput != nil {
		l.consoleOutput.Println(output)
	}

	if l.fileOutput != nil {
		l.fileOutput.Println(output)
	}
}

// formatSimpleLog creates a human-readable log format for non-JSON output
func (l *EPaxosLogger) formatSimpleLog(entry *StructuredLogEntry) string {
	base := fmt.Sprintf("[%s] [%s] [%s] [R%d] %s",
		entry.Timestamp.Format("2006-01-02 15:04:05.000"),
		entry.Level,
		entry.Category,
		entry.ReplicaID,
		entry.Message)

	// Add key contextual information
	if entry.InstanceID != nil {
		base += fmt.Sprintf(" | Instance: R%d.%d", *entry.TargetReplicaID, *entry.InstanceID)
	}
	if entry.Command != nil {
		base += fmt.Sprintf(" | Command: %s", formatCommand(*entry.Command))
	}
	if entry.Sequence != nil {
		base += fmt.Sprintf(" | Seq: %d", *entry.Sequence)
	}

	return base
}

// Convenience methods for backward compatibility
func (l *EPaxosLogger) Debug(category LogCategory, format string, args ...interface{}) {
	l.Log(DEBUG, category, fmt.Sprintf(format, args...)).Send()
}

func (l *EPaxosLogger) Info(category LogCategory, format string, args ...interface{}) {
	l.Log(INFO, category, fmt.Sprintf(format, args...)).Send()
}

func (l *EPaxosLogger) Warn(category LogCategory, format string, args ...interface{}) {
	l.Log(WARN, category, fmt.Sprintf(format, args...)).Send()
}

func (l *EPaxosLogger) Error(category LogCategory, format string, args ...interface{}) {
	l.Log(ERROR, category, fmt.Sprintf(format, args...)).Send()
}

func (l *EPaxosLogger) Fatal(category LogCategory, format string, args ...interface{}) {
	l.Log(FATAL, category, fmt.Sprintf(format, args...)).Send()
}

// SetLevel sets the logging level
func (l *EPaxosLogger) SetLevel(level LogLevel) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Close closes the logger and its file
func (l *EPaxosLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logFile != nil {
		return l.logFile.Close()
	}
	return nil
}

// RotateLog rotates the log file
func (l *EPaxosLogger) RotateLog(newPath string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.logFile == nil || l.fileOutput == nil {
		return fmt.Errorf("file logging not enabled")
	}

	// Close existing file
	if err := l.logFile.Close(); err != nil {
		return fmt.Errorf("failed to close existing log file: %w", err)
	}

	// Open new file
	file, err := os.OpenFile(newPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open new log file: %w", err)
	}

	l.logFile = file
	l.fileOutput = log.New(file, "", 0)

	return nil
}

// AddWriter adds an additional writer to the logger
func (l *EPaxosLogger) AddWriter(w io.Writer) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Create a multi-writer for console output
	if l.consoleOutput != nil {
		multiWriter := io.MultiWriter(os.Stdout, w)
		l.consoleOutput = log.New(multiWriter, "", 0)
	} else {
		l.consoleOutput = log.New(w, "", 0)
	}
}

// Helper function for formatting status
func formatStatus(status InstanceStatus) string {
	switch status {
	case StatusNone:
		return "NONE"
	case StatusPreAccepted:
		return "PRE-ACCEPTED"
	case StatusAccepted:
		return "ACCEPTED"
	case StatusCommitted:
		return "COMMITTED"
	case StatusExecuted:
		return "EXECUTED"
	default:
		return "UNKNOWN"
	}
}

// Helper function for formatting commands
func formatCommand(cmd Command) string {
	switch cmd.Type {
	case CmdGet:
		return fmt.Sprintf("GET(%s)", cmd.Key)
	case CmdPut:
		return fmt.Sprintf("PUT(%s, %s)", cmd.Key, cmd.Value)
	default:
		return "UNKNOWN"
	}
}

// global logger instance
var logger *EPaxosLogger
var loggerOnce sync.Once

// GetLogger returns the global logger instance
func GetLogger() *EPaxosLogger {
	return logger
}

// InitLogger initializes the global logger
func InitLogger(config LoggerConfig) error {
	var err error
	loggerOnce.Do(func() {
		logger, err = NewLogger(config)
	})
	return err
}
