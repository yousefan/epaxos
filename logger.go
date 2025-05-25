// logger.go
package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
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
)

// EPaxosLogger is a custom logger for the EPaxos system
type EPaxosLogger struct {
	mu            sync.Mutex
	level         LogLevel
	consoleOutput *log.Logger
	fileOutput    *log.Logger
	replicaID     ReplicaID
	logFile       *os.File
}

// LoggerConfig holds configuration for the logger
type LoggerConfig struct {
	Level         LogLevel
	ReplicaID     ReplicaID
	LogDir        string
	LogFileName   string
	ConsoleOutput bool
	FileOutput    bool
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
	}
}

// NewLogger creates a new EPaxos logger
func NewLogger(config LoggerConfig) (*EPaxosLogger, error) {
	logger := &EPaxosLogger{
		level:     config.Level,
		replicaID: config.ReplicaID,
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

// formatLogMessage formats a log message with timestamp, level, and category
func (l *EPaxosLogger) formatLogMessage(level LogLevel, category LogCategory, format string, args ...interface{}) string {
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	message := fmt.Sprintf(format, args...)

	// Get file and line number information
	_, file, line, ok := runtime.Caller(2) // Skip 2 frames to get the caller
	fileInfo := ""
	if ok {
		file = filepath.Base(file)
		fileInfo = fmt.Sprintf("%s:%d", file, line)
	}

	return fmt.Sprintf("[%s] [%s] [R%d] [%s] [%s] %s",
		timestamp, level.String(), l.replicaID, category, fileInfo, message)
}

// log logs a message with the given level and category
func (l *EPaxosLogger) log(level LogLevel, category LogCategory, format string, args ...interface{}) {
	if level < l.level {
		return
	}

	formattedMsg := l.formatLogMessage(level, category, format, args...)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.consoleOutput != nil {
		l.consoleOutput.Println(formattedMsg)
	}

	if l.fileOutput != nil {
		l.fileOutput.Println(formattedMsg)
	}

	if level == FATAL {
		os.Exit(1)
	}
}

// Debug logs a debug message
func (l *EPaxosLogger) Debug(category LogCategory, format string, args ...interface{}) {
	l.log(DEBUG, category, format, args...)
}

// Info logs an info message
func (l *EPaxosLogger) Info(category LogCategory, format string, args ...interface{}) {
	l.log(INFO, category, format, args...)
}

// Warn logs a warning message
func (l *EPaxosLogger) Warn(category LogCategory, format string, args ...interface{}) {
	l.log(WARN, category, format, args...)
}

// Error logs an error message
func (l *EPaxosLogger) Error(category LogCategory, format string, args ...interface{}) {
	l.log(ERROR, category, format, args...)
}

// Fatal logs a fatal message and exits the program
func (l *EPaxosLogger) Fatal(category LogCategory, format string, args ...interface{}) {
	l.log(FATAL, category, format, args...)
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
