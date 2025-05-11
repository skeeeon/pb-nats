package pbnats

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileManager handles operations on config files
type FileManager struct {
	configFile     string
	backupDir      string
	lastContentHash string
	maxBackups     int
	createBackups  bool
	logToConsole   bool
	mu             sync.Mutex
}

// NewFileManager creates a new FileManager
func NewFileManager(configFile string, backupDir string, maxBackups int, createBackups bool, logToConsole bool) *FileManager {
	// Create backup directory if it doesn't exist and backups are enabled
	if createBackups {
		if err := os.MkdirAll(backupDir, 0755); err != nil {
			log.Printf("Warning: Failed to create backup directory: %v", err)
		}
	}

	return &FileManager{
		configFile:    configFile,
		backupDir:     backupDir,
		maxBackups:    maxBackups,
		createBackups: createBackups,
		logToConsole:  logToConsole,
	}
}

// HasConfigChanged checks if the provided content is different from the current config file
func (fm *FileManager) HasConfigChanged(content string) (bool, error) {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Normalize content for consistent comparison
	normalizedContent := fm.normalizeContent(content)
	
	// Calculate hash of the normalized content
	contentHash := calculateHash(normalizedContent)
	
	// If we already checked this content and it's unchanged, skip
	if contentHash == fm.lastContentHash {
		return false, nil
	}
	
	// If the file doesn't exist, it has changed
	fileInfo, err := os.Stat(fm.configFile)
	if os.IsNotExist(err) {
		fm.lastContentHash = contentHash
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to stat config file: %w", err)
	}
	
	// If the file is empty, it has changed
	if fileInfo.Size() == 0 {
		fm.lastContentHash = contentHash
		return true, nil
	}
	
	// Read the current file content
	currentContent, err := os.ReadFile(fm.configFile)
	if err != nil {
		return false, fmt.Errorf("failed to read current config file: %w", err)
	}
	
	// Normalize the current content for comparison
	normalizedCurrentContent := fm.normalizeContent(string(currentContent))
	
	// Calculate hash of the normalized current content
	currentHash := calculateHash(normalizedCurrentContent)
	
	// Check if the content has changed
	hasChanged := currentHash != contentHash
	
	// Update last content hash
	fm.lastContentHash = contentHash
	
	if hasChanged && fm.logToConsole {
		log.Printf("NATS config content has changed, will update file")
	}
	
	return hasChanged, nil
}

// WriteConfigFile writes the content to the config file atomically
func (fm *FileManager) WriteConfigFile(content string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Create directory for config file if it doesn't exist
	configDir := filepath.Dir(fm.configFile)
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Create temporary file in the same directory as the target file
	tempFile, err := os.CreateTemp(configDir, "nats-config-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempFilePath := tempFile.Name()

	// Clean up the temporary file if something goes wrong
	defer func() {
		tempFile.Close()
		if _, err := os.Stat(tempFilePath); err == nil {
			os.Remove(tempFilePath)
		}
	}()

	// Write the content to the temporary file
	if _, err := tempFile.WriteString(content); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}

	// Close the file to ensure all data is flushed to disk
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Create a backup of the current config file if it exists and backups are enabled
	if fm.createBackups {
		if err := fm.backupCurrentConfig(); err != nil {
			log.Printf("Warning: Failed to create backup: %v", err)
			// Continue even if backup fails
		}
	}

	// Atomically rename the temporary file to the target file
	if err := os.Rename(tempFilePath, fm.configFile); err != nil {
		return fmt.Errorf("failed to replace config file: %w", err)
	}

	// Ensure proper file permissions
	if err := os.Chmod(fm.configFile, 0644); err != nil {
		log.Printf("Warning: Failed to set config file permissions: %v", err)
		// Continue even if permission setting fails
	}

	if fm.logToConsole {
		log.Printf("NATS config file updated successfully: %s", fm.configFile)
	}
	return nil
}

// backupCurrentConfig creates a backup of the current config file
func (fm *FileManager) backupCurrentConfig() error {
	// Check if the config file exists
	if _, err := os.Stat(fm.configFile); os.IsNotExist(err) {
		// No file to backup
		return nil
	}

	// Ensure backup directory exists
	if err := os.MkdirAll(fm.backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	backupFilename := filepath.Join(fm.backupDir, fmt.Sprintf("nats-config-%s.conf", timestamp))

	// Open source file
	source, err := os.Open(fm.configFile)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer source.Close()

	// Create destination file
	destination, err := os.Create(backupFilename)
	if err != nil {
		return fmt.Errorf("failed to create backup file: %w", err)
	}
	defer destination.Close()

	// Copy file contents
	if _, err := io.Copy(destination, source); err != nil {
		return fmt.Errorf("failed to copy file contents: %w", err)
	}

	// Cleanup old backups if maxBackups is set
	if fm.maxBackups > 0 {
		if err := fm.cleanupOldBackups(); err != nil {
			log.Printf("Warning: Failed to clean up old backups: %v", err)
		}
	}

	if fm.logToConsole {
		log.Printf("NATS config backup created: %s", backupFilename)
	}
	return nil
}

// cleanupOldBackups removes older backups when exceeding the maximum limit
func (fm *FileManager) cleanupOldBackups() error {
	// List all files in the backup directory
	files, err := os.ReadDir(fm.backupDir)
	if err != nil {
		return fmt.Errorf("failed to read backup directory: %w", err)
	}

	// Filter for backup files (nats-config-*.conf)
	var backups []string
	for _, file := range files {
		if !file.IsDir() && strings.HasPrefix(file.Name(), "nats-config-") && strings.HasSuffix(file.Name(), ".conf") {
			backups = append(backups, file.Name())
		}
	}

	// Sort backups by name (which includes timestamp) in descending order
	sort.Slice(backups, func(i, j int) bool {
		return backups[i] > backups[j]
	})

	// Remove excess backups
	if len(backups) > fm.maxBackups {
		for _, backup := range backups[fm.maxBackups:] {
			backupPath := filepath.Join(fm.backupDir, backup)
			if err := os.Remove(backupPath); err != nil {
				log.Printf("Warning: Failed to remove old backup %s: %v", backupPath, err)
			} else if fm.logToConsole {
				log.Printf("Removed old NATS config backup: %s", backupPath)
			}
		}
	}

	return nil
}

// normalizeContent normalizes the content for consistent comparison
// Removes comments and whitespace
func (fm *FileManager) normalizeContent(content string) string {
	var normalized bytes.Buffer
	for _, line := range strings.Split(content, "\n") {
		// Remove comments
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		
		// Trim whitespace
		line = strings.TrimSpace(line)
		
		// Skip empty lines
		if line == "" {
			continue
		}
		
		normalized.WriteString(line)
		normalized.WriteRune('\n')
	}
	return normalized.String()
}

// calculateHash calculates a SHA-256 hash of the content
func calculateHash(content string) string {
	hash := sha256.New()
	hash.Write([]byte(content))
	return hex.EncodeToString(hash.Sum(nil))
}
