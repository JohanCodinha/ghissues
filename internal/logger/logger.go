// Package logger provides a simple logging system with configurable levels and output.
package logger

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Level represents a log level.
type Level int

const (
	// LevelDebug is the most verbose log level.
	LevelDebug Level = iota
	// LevelInfo is the default log level for general information.
	LevelInfo
	// LevelWarn is for warning messages.
	LevelWarn
	// LevelError is for error messages only.
	LevelError
)

// String returns the string representation of a log level.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Logger is a simple logger with configurable level and output.
type Logger struct {
	mu     sync.Mutex
	level  Level
	output io.Writer
	file   *os.File // optional log file
}

var defaultLogger = &Logger{
	level:  LevelInfo,
	output: os.Stderr,
}

// SetLevel sets the minimum log level for the default logger.
func SetLevel(level Level) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = level
}

// SetOutput sets the output writer for the default logger.
// This is primarily useful for testing.
func SetOutput(w io.Writer) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

// SetLogFile opens a log file for writing in addition to the current output.
// The log file will receive all log messages that meet the level threshold.
// Returns an error if the file cannot be opened.
func SetLogFile(path string) error {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	// Close existing file if any
	if defaultLogger.file != nil {
		defaultLogger.file.Close()
		defaultLogger.file = nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	defaultLogger.file = f
	return nil
}

// Close closes the log file if one is open.
func Close() {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()

	if defaultLogger.file != nil {
		defaultLogger.file.Close()
		defaultLogger.file = nil
	}
}

// log writes a formatted log message if the level meets the threshold.
func (l *Logger) log(level Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.level {
		return
	}

	// Format: 2006-01-02T15:04:05.000Z LEVEL message
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	msg := fmt.Sprintf(format, args...)
	line := fmt.Sprintf("%s %s %s\n", timestamp, level.String(), msg)

	// Write to primary output
	io.WriteString(l.output, line)

	// Also write to file if configured
	if l.file != nil {
		io.WriteString(l.file, line)
	}
}

// Debug logs at debug level.
func Debug(format string, args ...interface{}) {
	defaultLogger.log(LevelDebug, format, args...)
}

// Info logs at info level.
func Info(format string, args ...interface{}) {
	defaultLogger.log(LevelInfo, format, args...)
}

// Warn logs at warn level.
func Warn(format string, args ...interface{}) {
	defaultLogger.log(LevelWarn, format, args...)
}

// Error logs at error level.
func Error(format string, args ...interface{}) {
	defaultLogger.log(LevelError, format, args...)
}

// ParseLevel converts a string to a Level.
// Accepts: debug, info, warn, error (case-insensitive).
// Returns an error for unknown level strings.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, nil
	case "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	default:
		return LevelInfo, fmt.Errorf("unknown log level %q: valid levels are debug, info, warn, error", s)
	}
}

// GetLevel returns the current log level of the default logger.
func GetLevel() Level {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	return defaultLogger.level
}
