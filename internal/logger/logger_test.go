package logger

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetLogger resets the default logger to a clean state for testing.
func resetLogger() {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = LevelInfo
	defaultLogger.output = os.Stderr
	if defaultLogger.file != nil {
		defaultLogger.file.Close()
		defaultLogger.file = nil
	}
}

func TestLogLevels(t *testing.T) {
	tests := []struct {
		name     string
		level    Level
		expected string
	}{
		{"debug", LevelDebug, "DEBUG"},
		{"info", LevelInfo, "INFO"},
		{"warn", LevelWarn, "WARN"},
		{"error", LevelError, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.level.String() != tt.expected {
				t.Errorf("Level.String() = %q, want %q", tt.level.String(), tt.expected)
			}
		})
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected Level
		wantErr  bool
	}{
		{"debug", LevelDebug, false},
		{"DEBUG", LevelDebug, false},
		{"  debug  ", LevelDebug, false},
		{"info", LevelInfo, false},
		{"INFO", LevelInfo, false},
		{"warn", LevelWarn, false},
		{"warning", LevelWarn, false},
		{"WARN", LevelWarn, false},
		{"error", LevelError, false},
		{"ERROR", LevelError, false},
		{"invalid", LevelInfo, true},
		{"", LevelInfo, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			level, err := ParseLevel(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseLevel(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && level != tt.expected {
				t.Errorf("ParseLevel(%q) = %v, want %v", tt.input, level, tt.expected)
			}
		})
	}
}

func TestLogOutput(t *testing.T) {
	resetLogger()
	defer resetLogger()

	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelDebug)

	Debug("test debug %s", "message")
	output := buf.String()

	if !strings.Contains(output, "DEBUG") {
		t.Errorf("Expected output to contain DEBUG, got: %s", output)
	}
	if !strings.Contains(output, "test debug message") {
		t.Errorf("Expected output to contain message, got: %s", output)
	}
	// Check timestamp format (YYYY-MM-DDTHH:MM:SS.sssZ)
	if !strings.Contains(output, "Z DEBUG") {
		t.Errorf("Expected ISO timestamp format, got: %s", output)
	}
}

func TestLevelFiltering(t *testing.T) {
	resetLogger()
	defer resetLogger()

	var buf bytes.Buffer
	SetOutput(&buf)

	// Set level to INFO - debug should be filtered
	SetLevel(LevelInfo)

	Debug("this should not appear")
	Info("this should appear")

	output := buf.String()

	if strings.Contains(output, "this should not appear") {
		t.Errorf("Debug message should be filtered at INFO level")
	}
	if !strings.Contains(output, "this should appear") {
		t.Errorf("Info message should not be filtered at INFO level")
	}
}

func TestAllLevelFunctions(t *testing.T) {
	resetLogger()
	defer resetLogger()

	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelDebug)

	Debug("debug msg")
	Info("info msg")
	Warn("warn msg")
	Error("error msg")

	output := buf.String()

	tests := []struct {
		level   string
		message string
	}{
		{"DEBUG", "debug msg"},
		{"INFO", "info msg"},
		{"WARN", "warn msg"},
		{"ERROR", "error msg"},
	}

	for _, tt := range tests {
		if !strings.Contains(output, tt.level) {
			t.Errorf("Expected output to contain %s level", tt.level)
		}
		if !strings.Contains(output, tt.message) {
			t.Errorf("Expected output to contain %q", tt.message)
		}
	}
}

func TestFileOutput(t *testing.T) {
	resetLogger()
	defer resetLogger()

	// Create a temp file for logging
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelInfo)

	err := SetLogFile(logPath)
	if err != nil {
		t.Fatalf("SetLogFile failed: %v", err)
	}

	Info("test file message")

	// Close to flush
	Close()

	// Check both outputs
	bufOutput := buf.String()
	if !strings.Contains(bufOutput, "test file message") {
		t.Errorf("Primary output should contain message, got: %s", bufOutput)
	}

	fileContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("Failed to read log file: %v", err)
	}
	if !strings.Contains(string(fileContent), "test file message") {
		t.Errorf("Log file should contain message, got: %s", fileContent)
	}
}

func TestSetLogFileError(t *testing.T) {
	resetLogger()
	defer resetLogger()

	// Try to open a file in a non-existent directory
	err := SetLogFile("/nonexistent/directory/test.log")
	if err == nil {
		t.Error("Expected error when opening file in non-existent directory")
	}
}

func TestGetLevel(t *testing.T) {
	resetLogger()
	defer resetLogger()

	SetLevel(LevelWarn)
	if GetLevel() != LevelWarn {
		t.Errorf("GetLevel() = %v, want %v", GetLevel(), LevelWarn)
	}

	SetLevel(LevelDebug)
	if GetLevel() != LevelDebug {
		t.Errorf("GetLevel() = %v, want %v", GetLevel(), LevelDebug)
	}
}

func TestConcurrentLogging(t *testing.T) {
	resetLogger()
	defer resetLogger()

	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelDebug)

	var wg sync.WaitGroup
	numGoroutines := 10
	numMessages := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numMessages; j++ {
				Info("goroutine %d message %d", id, j)
			}
		}(i)
	}

	wg.Wait()

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	expectedCount := numGoroutines * numMessages
	if len(lines) != expectedCount {
		t.Errorf("Expected %d log lines, got %d", expectedCount, len(lines))
	}
}

func TestLevelUnknown(t *testing.T) {
	// Test an invalid level value
	var level Level = 999
	if level.String() != "UNKNOWN" {
		t.Errorf("Unknown level should return UNKNOWN, got: %s", level.String())
	}
}

func TestCloseWithNoFile(t *testing.T) {
	resetLogger()
	defer resetLogger()

	// Close should not panic when no file is open
	Close()
}

func TestSetLogFileReplacesExisting(t *testing.T) {
	resetLogger()
	defer resetLogger()

	tmpDir := t.TempDir()
	logPath1 := filepath.Join(tmpDir, "test1.log")
	logPath2 := filepath.Join(tmpDir, "test2.log")

	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelInfo)

	// Set first log file
	err := SetLogFile(logPath1)
	if err != nil {
		t.Fatalf("SetLogFile(1) failed: %v", err)
	}

	Info("message to file 1")

	// Set second log file (should close first)
	err = SetLogFile(logPath2)
	if err != nil {
		t.Fatalf("SetLogFile(2) failed: %v", err)
	}

	Info("message to file 2")

	Close()

	// Check file 1 has first message
	content1, _ := os.ReadFile(logPath1)
	if !strings.Contains(string(content1), "message to file 1") {
		t.Errorf("Log file 1 should contain first message")
	}
	if strings.Contains(string(content1), "message to file 2") {
		t.Errorf("Log file 1 should NOT contain second message")
	}

	// Check file 2 has second message
	content2, _ := os.ReadFile(logPath2)
	if !strings.Contains(string(content2), "message to file 2") {
		t.Errorf("Log file 2 should contain second message")
	}
	if strings.Contains(string(content2), "message to file 1") {
		t.Errorf("Log file 2 should NOT contain first message")
	}
}
