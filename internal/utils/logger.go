// Package utils provides utility functions for the pb-nats library
package utils

import (
	"fmt"
	"log"
	"time"
)

// Logger provides consistent logging throughout the pb-nats library
type Logger struct {
	enabled bool
}

// NewLogger creates a new logger instance
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled: enabled,
	}
}

// logWithPrefix writes a log message with a prefix if logging is enabled
func (l *Logger) logWithPrefix(prefix, format string, args ...interface{}) {
	if !l.enabled {
		return
	}
	
	timestamp := time.Now().Format("15:04:05")
	message := fmt.Sprintf(format, args...)
	log.Printf("[%s] %s %s", timestamp, prefix, message)
}

// Start logs a startup message
func (l *Logger) Start(format string, args ...interface{}) {
	l.logWithPrefix("🚀 START", format, args...)
}

// Success logs a success message
func (l *Logger) Success(format string, args ...interface{}) {
	l.logWithPrefix("✅ SUCCESS", format, args...)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	l.logWithPrefix("ℹ️  INFO", format, args...)
}

// Process logs a processing message
func (l *Logger) Process(format string, args ...interface{}) {
	l.logWithPrefix("⚙️  PROCESS", format, args...)
}

// Warning logs a warning message
func (l *Logger) Warning(format string, args ...interface{}) {
	l.logWithPrefix("⚠️  WARNING", format, args...)
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	l.logWithPrefix("❌ ERROR", format, args...)
}

// Stop logs a shutdown message
func (l *Logger) Stop(format string, args ...interface{}) {
	l.logWithPrefix("🛑 STOP", format, args...)
}

// Publish logs a publishing operation
func (l *Logger) Publish(format string, args ...interface{}) {
	l.logWithPrefix("📤 PUBLISH", format, args...)
}

// Delete logs a deletion operation
func (l *Logger) Delete(format string, args ...interface{}) {
	l.logWithPrefix("🗑️  DELETE", format, args...)
}
