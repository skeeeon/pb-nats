package pbnats

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Reloader handles reloading the NATS server configuration
type Reloader struct {
	reloadCommand string
	lastReload    time.Time
	minInterval   time.Duration
	logToConsole  bool
	mu            sync.Mutex
}

// NewReloader creates a new NATS configuration reloader
func NewReloader(reloadCommand string, minInterval time.Duration, logToConsole bool) *Reloader {
	return &Reloader{
		reloadCommand: reloadCommand,
		minInterval:   minInterval,
		logToConsole:  logToConsole,
	}
}

// ReloadConfig triggers a reload of the NATS server configuration
func (r *Reloader) ReloadConfig() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Check if we've reloaded too recently
	if time.Since(r.lastReload) < r.minInterval {
		if r.logToConsole {
			log.Printf("Skipping NATS reload, too soon since last reload")
		}
		return nil
	}

	// Split the command into command and arguments
	parts := strings.Fields(r.reloadCommand)
	if len(parts) == 0 {
		return fmt.Errorf("empty reload command")
	}

	cmdName := parts[0]
	var cmdArgs []string
	if len(parts) > 1 {
		cmdArgs = parts[1:]
	}

	// Execute the command
	cmd := exec.Command(cmdName, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("reload command failed: %w, output: %s", err, string(output))
	}

	// Update last reload time
	r.lastReload = time.Now()

	if r.logToConsole {
		log.Printf("NATS server configuration reloaded successfully")
	}
	return nil
}
