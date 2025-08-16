// Package utils provides utility functions for the pb-nats library
package utils

import (
	"fmt"
	"log"
	"time"
)

// Logger provides consistent, categorized logging throughout the pb-nats library.
// This component standardizes log output with visual prefixes, timestamps, and
// semantic categorization for operational monitoring and debugging.
//
// LOGGING PHILOSOPHY:
// - Visual consistency with emoji prefixes for quick status recognition
// - Semantic categorization (start, success, info, warning, error, etc.)
// - Timestamp precision for debugging timing issues
// - Toggle capability for production vs development environments
// - Operational focus (what the system is doing, not just errors)
//
// PREFIX SEMANTICS:
// Each log level has a distinct visual prefix to enable rapid scanning:
// - 🚀 START: System initialization and component startup
// - ✅ SUCCESS: Successful completion of operations
// - ℹ️ INFO: Informational messages and state changes
// - ⚙️ PROCESS: Active processing and background operations
// - ⚠️ WARNING: Recoverable issues that need attention
// - ❌ ERROR: Failures and error conditions
// - 🛑 STOP: Shutdown and cleanup operations
// - 📤 PUBLISH: NATS publishing operations
// - 🗑️ DELETE: Resource removal operations
//
// OPERATIONAL INTEGRATION:
// Designed for pb-nats operational needs including bootstrap monitoring,
// connection state tracking, queue processing, and error troubleshooting.
type Logger struct {
	enabled bool // Controls whether log messages are output
}

// NewLogger creates a new logger instance with configurable output.
//
// ENABLE/DISABLE LOGIC:
// - enabled=true: Full logging for development and debugging
// - enabled=false: Silent operation for production or testing
//
// PARAMETERS:
//   - enabled: Whether to output log messages
//
// RETURNS:
// - Logger instance ready for categorized logging
//
// USAGE PATTERNS:
// - Development: NewLogger(true) for full visibility
// - Production: NewLogger(options.LogToConsole) for configurable logging
// - Testing: NewLogger(false) for silent operation
func NewLogger(enabled bool) *Logger {
	return &Logger{
		enabled: enabled,
	}
}

// logWithPrefix writes a log message with timestamp and visual prefix if logging enabled.
// This is the core logging method that handles formatting consistency.
//
// LOG FORMAT:
//   [HH:MM:SS] PREFIX MESSAGE
//   [15:04:05] 🚀 START PocketBase NATS JWT sync initialized
//
// TIMESTAMP FORMAT:
// Uses HH:MM:SS format for human readability and space efficiency.
// Sufficient precision for operational debugging without date clutter.
//
// CONDITIONAL OUTPUT:
// Only outputs if logger is enabled, making it safe to call in all
// code paths without performance impact when logging is disabled.
//
// PARAMETERS:
//   - prefix: Visual prefix with emoji and category name
//   - format: Printf-style format string
//   - args: Format arguments
//
// SIDE EFFECTS:
// - Writes to standard log output if enabled
// - No output if logger disabled
func (l *Logger) logWithPrefix(prefix, format string, args ...interface{}) {
	if !l.enabled {
		return
	}
	
	timestamp := time.Now().Format("15:04:05")
	message := fmt.Sprintf(format, args...)
	log.Printf("[%s] %s %s", timestamp, prefix, message)
}

// Start logs system initialization and component startup messages.
// Use for major system milestones and component initialization.
//
// WHEN TO USE:
// - System boot and initialization sequences
// - Major component startup (connection manager, publisher, sync hooks)
// - Service initialization milestones
// - Configuration loading and validation
//
// VISUAL PREFIX: 🚀 START
//
// EXAMPLES:
//   logger.Start("PocketBase NATS JWT sync initialized")
//   logger.Start("Starting account publisher with connection manager")
//   logger.Start("Initializing NATS sync components...")
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Start(format string, args ...interface{}) {
	l.logWithPrefix("🚀 START", format, args...)
}

// Success logs successful completion of operations and positive outcomes.
// Use for confirming important operations completed successfully.
//
// WHEN TO USE:
// - Successful system initialization
// - Connection establishment and reconnection
// - Successful NATS operations (publish, failover, bootstrap exit)
// - Queue processing completion
// - Configuration validation success
//
// VISUAL PREFIX: ✅ SUCCESS
//
// EXAMPLES:
//   logger.Success("Connected to NATS server: %s", url)
//   logger.Success("Bootstrap successful! Connected to NATS")
//   logger.Success("Queue processing complete: %d processed", count)
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Success(format string, args ...interface{}) {
	l.logWithPrefix("✅ SUCCESS", format, args...)
}

// Info logs informational messages and state changes.
// Use for important system state information and context.
//
// WHEN TO USE:
// - Configuration details and system settings
// - State transitions (bootstrap mode, connection status)
// - Operational context (retry attempts, queue depths)
// - Resource counts and statistics
// - Non-critical system events
//
// VISUAL PREFIX: ℹ️ INFO
//
// EXAMPLES:
//   logger.Info("Primary NATS server: %s", url)
//   logger.Info("Bootstrap mode: waiting for NATS connection")
//   logger.Info("Signing keys rotated for account %s", name)
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Info(format string, args ...interface{}) {
	l.logWithPrefix("ℹ️  INFO", format, args...)
}

// Process logs active processing and background operations.
// Use for ongoing work and batch operations.
//
// WHEN TO USE:
// - Background processing activities
// - Batch operations (queue processing, cleanup)
// - Long-running operations with progress updates
// - Multi-step operation progress
// - Resource processing (multiple records, connections)
//
// VISUAL PREFIX: ⚙️ PROCESS
//
// EXAMPLES:
//   logger.Process("Processing %d queued publish operations...", count)
//   logger.Process("Initializing NATS sync components...")
//   logger.Process("Regenerating JWTs for users with role %s", roleID)
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Process(format string, args ...interface{}) {
	l.logWithPrefix("⚙️  PROCESS", format, args...)
}

// Warning logs recoverable issues that need attention.
// Use for problems that don't stop operation but indicate potential issues.
//
// WHEN TO USE:
// - Recoverable connection failures
// - Retry attempts and backoff operations
// - Configuration issues with fallbacks
// - Performance concerns (slow operations, high resource usage)
// - Deprecated usage or suboptimal conditions
// - Bootstrap mode announcements
//
// VISUAL PREFIX: ⚠️ WARNING
//
// EXAMPLES:
//   logger.Warning("Primary connection failed, attempting failover: %v", err)
//   logger.Warning("Failed to process queue record %s: %v", recordID, err)
//   logger.Warning("All servers unavailable, entering bootstrap mode")
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Warning(format string, args ...interface{}) {
	l.logWithPrefix("⚠️  WARNING", format, args...)
}

// Error logs failures and error conditions.
// Use for serious problems that affect system functionality.
//
// WHEN TO USE:
// - Component initialization failures
// - Database operation errors
// - Configuration validation failures
// - Unrecoverable operation failures
// - System-level errors requiring intervention
//
// VISUAL PREFIX: ❌ ERROR
//
// EXAMPLES:
//   logger.Error("NATS sync initialization failed: %v", err)
//   logger.Error("Failed to save queue record: %v", err)
//   logger.Error("Invalid system operator configuration")
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Error(format string, args ...interface{}) {
	l.logWithPrefix("❌ ERROR", format, args...)
}

// Stop logs shutdown and cleanup operations.
// Use for graceful shutdown sequences and resource cleanup.
//
// WHEN TO USE:
// - System shutdown initiation
// - Component cleanup and resource release
// - Connection closure and cleanup
// - Background process termination
// - Graceful degradation announcements
//
// VISUAL PREFIX: 🛑 STOP
//
// EXAMPLES:
//   logger.Stop("Stopping NATS account publisher...")
//   logger.Stop("NATS connection manager closed")
//   logger.Stop("Queue processor shutting down...")
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Stop(format string, args ...interface{}) {
	l.logWithPrefix("🛑 STOP", format, args...)
}

// Publish logs NATS publishing operations and account updates.
// Use for tracking NATS server synchronization activities.
//
// WHEN TO USE:
// - Account JWT publishing to NATS
// - NATS claims updates ($SYS.REQ.CLAIMS.UPDATE)
// - Successful NATS synchronization operations
// - Publishing operation confirmations
// - NATS server response acknowledgments
//
// VISUAL PREFIX: 📤 PUBLISH
//
// EXAMPLES:
//   logger.Publish("Published account %s to NATS: %s", name, response)
//   logger.Publish("Account JWT updated in NATS resolver")
//   logger.Publish("Queue operation published successfully")
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Publish(format string, args ...interface{}) {
	l.logWithPrefix("📤 PUBLISH", format, args...)
}

// Delete logs resource removal operations and cleanup activities.
// Use for tracking deletion and cleanup operations.
//
// WHEN TO USE:
// - Account removal from NATS
// - Queue record deletion
// - Resource cleanup and garbage collection
// - Failed record cleanup operations
// - NATS claims deletion ($SYS.REQ.CLAIMS.DELETE)
//
// VISUAL PREFIX: 🗑️ DELETE
//
// EXAMPLES:
//   logger.Delete("Removed account %s from NATS: %s", name, response)
//   logger.Delete("Account %s not found, removing queue record", accountID)
//   logger.Delete("Cleanup completed: %d old failed records removed", count)
//
// PARAMETERS:
//   - format: Printf-style format string
//   - args: Format arguments
func (l *Logger) Delete(format string, args ...interface{}) {
	l.logWithPrefix("🗑️  DELETE", format, args...)
}
