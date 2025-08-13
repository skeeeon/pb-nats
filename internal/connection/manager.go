// Package connection provides persistent NATS connection management with failover
package connection

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// FailoverState represents the current state of the connection manager
type FailoverState int

const (
	StateHealthy FailoverState = iota
	StateRetrying
	StateFailedOver
	StateBootstrapping // New state for initial bootstrap
)

// String returns the string representation of the failover state
func (s FailoverState) String() string {
	switch s {
	case StateHealthy:
		return "healthy"
	case StateRetrying:
		return "retrying"
	case StateFailedOver:
		return "failed_over"
	case StateBootstrapping:
		return "bootstrapping"
	default:
		return "unknown"
	}
}

// ConnectionCredentials holds the JWT and seed for NATS authentication
type ConnectionCredentials struct {
	JWT  string
	Seed string
}

// ConnectionStatus provides information about the current connection state
type ConnectionStatus struct {
	Connected     bool
	CurrentURL    string
	FailoverState FailoverState
	LastFailover  *time.Time
}

// Manager handles persistent NATS connections with failover and graceful bootstrap
type Manager struct {
	// Configuration
	primaryURL   string
	backupURLs   []string
	credentials  *ConnectionCredentials
	retryConfig  *pbtypes.RetryConfig
	timeouts     *pbtypes.TimeoutConfig

	// Connection state
	conn       *nats.Conn
	currentURL string
	mu         sync.RWMutex

	// Failover state
	failoverState  FailoverState
	lastFailover   time.Time
	failbackTicker *time.Ticker

	// Bootstrap state
	isBootstrapping    bool
	bootstrapRetryTicker *time.Ticker
	
	// Context and cleanup
	ctx       context.Context
	cancelCtx context.CancelFunc

	// Utilities
	logger *utils.Logger
}

// NewManager creates a new connection manager with the specified configuration
func NewManager(primaryURL string, backupURLs []string, retryConfig *pbtypes.RetryConfig, 
	timeouts *pbtypes.TimeoutConfig, logger *utils.Logger) *Manager {
	
	ctx, cancel := context.WithCancel(context.Background())

	// Apply defaults if not provided
	if retryConfig == nil {
		retryConfig = pbtypes.DefaultRetryConfig()
	}
	if timeouts == nil {
		timeouts = pbtypes.DefaultTimeoutConfig()
	}

	return &Manager{
		primaryURL:      primaryURL,
		backupURLs:      backupURLs,
		retryConfig:     retryConfig,
		timeouts:        timeouts,
		failoverState:   StateBootstrapping,
		isBootstrapping: true,
		ctx:             ctx,
		cancelCtx:       cancel,
		logger:          logger,
	}
}

// StartBootstrap begins the bootstrap process - tries to connect but doesn't fail if unsuccessful
func (cm *Manager) StartBootstrap(jwt, seed string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update credentials
	cm.credentials = &ConnectionCredentials{
		JWT:  jwt,
		Seed: seed,
	}

	// Start background connection attempts
	cm.startBootstrapRetry()
	
	cm.logger.Info("Bootstrap mode: NATS connection will be established when server becomes available")
}

// Connect establishes connection immediately (for when NATS is known to be available)
func (cm *Manager) Connect(jwt, seed string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update credentials
	cm.credentials = &ConnectionCredentials{
		JWT:  jwt,
		Seed: seed,
	}

	// If we're in bootstrap mode, exit bootstrap and connect normally
	if cm.isBootstrapping {
		cm.exitBootstrapMode()
	}

	// Try to connect
	conn, url, err := cm.tryAllServers()
	if err != nil {
		// If this fails, go back to bootstrap mode instead of failing
		cm.logger.Warning("Initial connection failed, entering bootstrap mode: %v", err)
		cm.enterBootstrapMode()
		return nil // Don't return error - use bootstrap mode instead
	}

	cm.conn = conn
	cm.currentURL = url
	cm.failoverState = StateHealthy

	// Start failback monitoring if we're not on primary
	if url != cm.primaryURL {
		cm.failoverState = StateFailedOver
		cm.lastFailover = time.Now()
		cm.startFailbackMonitoring()
	}

	cm.logger.Success("Connected to NATS server: %s", url)
	return nil
}

// Close closes the connection and stops background monitoring
func (cm *Manager) Close() error {
	// Cancel context first to signal all goroutines to exit
	cm.cancelCtx()

	// Give goroutines a moment to clean up gracefully
	time.Sleep(100 * time.Millisecond)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Stop failback ticker safely
	if cm.failbackTicker != nil {
		ticker := cm.failbackTicker
		cm.failbackTicker = nil
		ticker.Stop()
	}

	// Stop bootstrap retry ticker safely
	if cm.bootstrapRetryTicker != nil {
		ticker := cm.bootstrapRetryTicker
		cm.bootstrapRetryTicker = nil
		ticker.Stop()
	}

	// Close NATS connection
	if cm.conn != nil {
		cm.conn.Close()
		cm.conn = nil
	}

	cm.logger.Info("NATS connection manager closed")
	return nil
}

// Publish publishes data to the specified subject with failover support
func (cm *Manager) Publish(subject string, data []byte) error {
	return cm.publishWithFailover(subject, data)
}

// Request sends a request and waits for a response with failover support
func (cm *Manager) Request(subject string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	return cm.requestWithFailover(subject, data, timeout)
}

// UpdateCredentials updates the authentication credentials and reconnects if needed
func (cm *Manager) UpdateCredentials(jwt, seed string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Update credentials
	cm.credentials = &ConnectionCredentials{
		JWT:  jwt,
		Seed: seed,
	}

	// If we're in bootstrap mode, try to establish connection now
	if cm.isBootstrapping {
		cm.logger.Info("Credentials updated, attempting to exit bootstrap mode...")
		if conn, url, err := cm.tryAllServers(); err == nil {
			cm.conn = conn
			cm.currentURL = url
			cm.exitBootstrapMode()
			cm.logger.Success("Exited bootstrap mode, connected to: %s", url)
			return nil
		}
	}

	// If we have an active connection, we need to reconnect with new credentials
	if cm.conn != nil && cm.conn.IsConnected() {
		cm.logger.Info("Updating NATS credentials, reconnecting...")
		
		// Close current connection
		cm.conn.Close()
		
		// Reconnect with new credentials
		conn, url, err := cm.tryAllServers()
		if err != nil {
			cm.conn = nil
			// Go back to bootstrap mode if reconnection fails
			cm.enterBootstrapMode()
			return nil // Don't return error - graceful degradation
		}

		cm.conn = conn
		cm.currentURL = url
		cm.failoverState = StateHealthy

		cm.logger.Success("Reconnected with updated credentials to: %s", url)
	}

	return nil
}

// GetConnectionStatus returns the current connection status
func (cm *Manager) GetConnectionStatus() ConnectionStatus {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	connected := cm.conn != nil && cm.conn.IsConnected()
	var lastFailover *time.Time
	if !cm.lastFailover.IsZero() {
		lastFailover = &cm.lastFailover
	}

	return ConnectionStatus{
		Connected:     connected,
		CurrentURL:    cm.currentURL,
		FailoverState: cm.failoverState,
		LastFailover:  lastFailover,
	}
}

// IsBootstrapping returns whether the connection manager is in bootstrap mode
func (cm *Manager) IsBootstrapping() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.isBootstrapping
}

// publishWithFailover attempts to publish with automatic failover or bootstrap handling
func (cm *Manager) publishWithFailover(subject string, data []byte) error {
	cm.mu.RLock()
	conn := cm.conn
	bootstrapping := cm.isBootstrapping
	cm.mu.RUnlock()

	// If we're in bootstrap mode, return a specific error
	if bootstrapping {
		return fmt.Errorf("NATS connection not available (bootstrap mode) - operation will be queued")
	}

	if conn == nil {
		return fmt.Errorf("no active NATS connection")
	}

	// Try publishing
	err := conn.Publish(subject, data)
	if err == nil {
		return nil
	}

	// Check if we should attempt failover
	if cm.isRetryableError(err) {
		cm.logger.Warning("Publish failed, attempting failover: %v", err)
		
		if reconnectErr := cm.handleConnectionFailure(err); reconnectErr != nil {
			return fmt.Errorf("publish failed and failover unsuccessful: %v", reconnectErr)
		}

		// Retry with new connection
		cm.mu.RLock()
		conn = cm.conn
		cm.mu.RUnlock()

		if conn != nil {
			retryErr := conn.Publish(subject, data)
			if retryErr == nil {
				cm.logger.Success("Publish succeeded after failover")
				return nil
			}
			return fmt.Errorf("publish failed even after failover: %v", retryErr)
		}
	}

	return fmt.Errorf("publish failed: %v", err)
}

// requestWithFailover attempts to send a request with automatic failover or bootstrap handling
func (cm *Manager) requestWithFailover(subject string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	cm.mu.RLock()
	conn := cm.conn
	bootstrapping := cm.isBootstrapping
	cm.mu.RUnlock()

	// If we're in bootstrap mode, return a specific error
	if bootstrapping {
		return nil, fmt.Errorf("NATS connection not available (bootstrap mode) - operation will be queued")
	}

	if conn == nil {
		return nil, fmt.Errorf("no active NATS connection")
	}

	// Try request
	msg, err := conn.Request(subject, data, timeout)
	if err == nil {
		return msg, nil
	}

	// Check if we should attempt failover
	if cm.isRetryableError(err) {
		cm.logger.Warning("Request failed, attempting failover: %v", err)
		
		if reconnectErr := cm.handleConnectionFailure(err); reconnectErr != nil {
			return nil, fmt.Errorf("request failed and failover unsuccessful: %v", reconnectErr)
		}

		// Retry with new connection
		cm.mu.RLock()
		conn = cm.conn
		cm.mu.RUnlock()

		if conn != nil {
			msg, retryErr := conn.Request(subject, data, timeout)
			if retryErr == nil {
				cm.logger.Success("Request succeeded after failover")
				return msg, nil
			}
			return nil, fmt.Errorf("request failed even after failover: %v", retryErr)
		}
	}

	return nil, fmt.Errorf("request failed: %v", err)
}

// Bootstrap mode management

// enterBootstrapMode puts the connection manager into bootstrap mode
func (cm *Manager) enterBootstrapMode() {
	cm.isBootstrapping = true
	cm.failoverState = StateBootstrapping
	cm.currentURL = ""
	
	// Close existing connection
	if cm.conn != nil {
		cm.conn.Close()
		cm.conn = nil
	}

	// Stop failback monitoring safely
	if cm.failbackTicker != nil {
		// Store reference and clear field first to prevent race
		ticker := cm.failbackTicker
		cm.failbackTicker = nil
		
		// Stop ticker - safe to do since we have local reference
		ticker.Stop()
	}

	// Start bootstrap retry
	cm.startBootstrapRetry()
	
	cm.logger.Info("Entered bootstrap mode - will retry connection when NATS becomes available")
}

// exitBootstrapMode exits bootstrap mode and enters normal operation
func (cm *Manager) exitBootstrapMode() {
	cm.isBootstrapping = false
	cm.failoverState = StateHealthy
	
	// Stop bootstrap retry ticker safely
	if cm.bootstrapRetryTicker != nil {
		// Store reference and clear field first to prevent race
		ticker := cm.bootstrapRetryTicker
		cm.bootstrapRetryTicker = nil
		
		// Stop ticker - safe to do since we have local reference
		ticker.Stop()
	}

	cm.logger.Success("Exited bootstrap mode - entering normal operation")
}

// startBootstrapRetry starts background connection attempts during bootstrap
func (cm *Manager) startBootstrapRetry() {
	// Stop existing ticker if running
	if cm.bootstrapRetryTicker != nil {
		cm.bootstrapRetryTicker.Stop()
		cm.bootstrapRetryTicker = nil
	}

	// Use a longer interval for bootstrap attempts to be less aggressive
	bootstrapInterval := cm.retryConfig.FailbackInterval
	if bootstrapInterval < 10*time.Second {
		bootstrapInterval = 10 * time.Second // Minimum 10 second intervals for bootstrap
	}

	// Create ticker
	ticker := time.NewTicker(bootstrapInterval)
	cm.bootstrapRetryTicker = ticker
	
	go func() {
		defer func() {
			// Safely stop the ticker - use local reference to avoid race condition
			ticker.Stop()
		}()
		
		for {
			select {
			case <-cm.ctx.Done():
				return
			case <-ticker.C:
				cm.tryBootstrapConnection()
			}
		}
	}()

	cm.logger.Info("Started bootstrap connection attempts (interval: %v)", bootstrapInterval)
}

// tryBootstrapConnection attempts to establish connection during bootstrap
func (cm *Manager) tryBootstrapConnection() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Only try if we're still in bootstrap mode
	if !cm.isBootstrapping || cm.credentials == nil {
		return
	}

	// Try to connect
	conn, url, err := cm.tryAllServers()
	if err != nil {
		// Log only at debug level to avoid spam during bootstrap
		cm.logger.Info("Bootstrap connection attempt failed (will retry): %v", err)
		return
	}

	// Success! Exit bootstrap mode
	cm.conn = conn
	cm.currentURL = url
	cm.exitBootstrapMode()

	// Start failback monitoring if we're not on primary
	if url != cm.primaryURL {
		cm.failoverState = StateFailedOver
		cm.lastFailover = time.Now()
		cm.startFailbackMonitoring()
	}

	cm.logger.Success("Bootstrap successful! Connected to NATS server: %s", url)
}

// handleConnectionFailure handles connection failures and attempts failover
func (cm *Manager) handleConnectionFailure(err error) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// If we're in bootstrap mode, don't attempt failover
	if cm.isBootstrapping {
		return fmt.Errorf("connection failure during bootstrap: %v", err)
	}

	// Set state to retrying
	cm.failoverState = StateRetrying

	// Try primary first with retries
	if cm.currentURL == cm.primaryURL {
		cm.logger.Info("Primary connection failed, retrying with backoff...")
		
		for attempt := 1; attempt <= cm.retryConfig.MaxPrimaryRetries; attempt++ {
			// Calculate backoff
			backoff := cm.calculateBackoff(attempt)
			cm.logger.Info("Retrying primary connection: attempt %d/%d (backoff: %v)", 
				attempt, cm.retryConfig.MaxPrimaryRetries, backoff)

			select {
			case <-cm.ctx.Done():
				return fmt.Errorf("context cancelled during retry")
			case <-time.After(backoff):
				// Try reconnecting to primary
				if conn, err := cm.connectToURL(cm.primaryURL); err == nil {
					if cm.conn != nil {
						cm.conn.Close()
					}
					cm.conn = conn
					cm.currentURL = cm.primaryURL
					cm.failoverState = StateHealthy
					cm.logger.Success("Reconnected to primary server after retry")
					return nil
				}
			}
		}
	}

	// Primary retries exhausted or we were already on backup, try failover
	return cm.attemptFailover()
}

// attemptFailover attempts to failover to backup servers or enter bootstrap mode
func (cm *Manager) attemptFailover() error {
	cm.logger.Warning("Attempting failover to backup servers...")

	// Close current connection
	if cm.conn != nil {
		cm.conn.Close()
		cm.conn = nil
	}

	// Try all servers (including primary at the end)
	conn, url, err := cm.tryAllServers()
	if err != nil {
		// If all servers fail, enter bootstrap mode instead of failing
		cm.logger.Warning("All servers unavailable, entering bootstrap mode: %v", err)
		cm.enterBootstrapMode()
		return nil // Don't return error - graceful degradation
	}

	cm.conn = conn
	cm.currentURL = url
	cm.lastFailover = time.Now()

	if url == cm.primaryURL {
		cm.failoverState = StateHealthy
		cm.logger.Success("Failed back to primary server during failover")
	} else {
		cm.failoverState = StateFailedOver
		cm.logger.Warning("Failed over to backup server: %s", url)
		cm.startFailbackMonitoring()
	}

	return nil
}

// tryAllServers tries to connect to servers in priority order: backups first, then primary
func (cm *Manager) tryAllServers() (*nats.Conn, string, error) {
	if cm.credentials == nil {
		return nil, "", fmt.Errorf("no credentials available")
	}

	// Try backup servers first
	for _, url := range cm.backupURLs {
		if conn, err := cm.connectToURL(url); err == nil {
			return conn, url, nil
		} else {
			cm.logger.Warning("Failed to connect to backup server %s: %v", url, err)
		}
	}

	// Try primary server last
	if conn, err := cm.connectToURL(cm.primaryURL); err == nil {
		return conn, cm.primaryURL, nil
	} else {
		cm.logger.Warning("Failed to connect to primary server %s: %v", cm.primaryURL, err)
	}

	return nil, "", fmt.Errorf("failed to connect to any NATS server")
}

// connectToURL attempts to connect to a specific NATS URL
func (cm *Manager) connectToURL(url string) (*nats.Conn, error) {
	options := []nats.Option{
		nats.UserJWTAndSeed(cm.credentials.JWT, cm.credentials.Seed),
		nats.Timeout(cm.timeouts.ConnectTimeout),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1), // Allow unlimited reconnection attempts
		nats.DisconnectErrHandler(cm.handleDisconnect),
		nats.ReconnectHandler(cm.handleReconnect),
		nats.ClosedHandler(cm.handleClosed),
	}

	return nats.Connect(url, options...)
}

// startFailbackMonitoring starts background monitoring to fail back to primary
func (cm *Manager) startFailbackMonitoring() {
	// Stop existing ticker if running
	if cm.failbackTicker != nil {
		cm.failbackTicker.Stop()
		cm.failbackTicker = nil
	}

	// Create ticker
	ticker := time.NewTicker(cm.retryConfig.FailbackInterval)
	cm.failbackTicker = ticker
	
	go func() {
		defer func() {
			// Safely stop the ticker - use local reference to avoid race condition
			ticker.Stop()
		}()
		
		for {
			select {
			case <-cm.ctx.Done():
				return
			case <-ticker.C:
				cm.checkPrimaryHealth()
			}
		}
	}()

	cm.logger.Info("Started failback monitoring (interval: %v)", cm.retryConfig.FailbackInterval)
}

// checkPrimaryHealth checks if the primary server is healthy and fails back if possible
func (cm *Manager) checkPrimaryHealth() {
	cm.mu.RLock()
	currentURL := cm.currentURL
	state := cm.failoverState
	bootstrapping := cm.isBootstrapping
	cm.mu.RUnlock()

	// Only check if we're currently failed over (not bootstrapping)
	if bootstrapping || currentURL == cm.primaryURL || state != StateFailedOver {
		return
	}

	// Try connecting to primary
	testConn, err := cm.connectToURL(cm.primaryURL)
	if err != nil {
		// Primary still not healthy
		return
	}

	// Primary is healthy, attempt failback
	cm.logger.Info("Primary server healthy, attempting failback...")
	
	cm.mu.Lock()
	// Close current connection
	if cm.conn != nil {
		cm.conn.Close()
	}
	
	// Switch to primary
	cm.conn = testConn
	cm.currentURL = cm.primaryURL
	cm.failoverState = StateHealthy
	cm.lastFailover = time.Time{} // Clear last failover time
	
	// Stop failback monitoring safely
	if cm.failbackTicker != nil {
		// Store reference and clear field first to prevent race
		ticker := cm.failbackTicker
		cm.failbackTicker = nil
		cm.mu.Unlock()
		
		// Stop ticker outside of lock to prevent deadlock
		ticker.Stop()
		
		cm.logger.Success("Failed back to primary server: %s", cm.primaryURL)
		return
	}
	cm.mu.Unlock()

	cm.logger.Success("Failed back to primary server: %s", cm.primaryURL)
}

// calculateBackoff calculates exponential backoff delay
func (cm *Manager) calculateBackoff(attempt int) time.Duration {
	backoff := cm.retryConfig.InitialBackoff
	for i := 1; i < attempt; i++ {
		backoff = time.Duration(float64(backoff) * cm.retryConfig.BackoffMultiplier)
		if backoff > cm.retryConfig.MaxBackoff {
			backoff = cm.retryConfig.MaxBackoff
			break
		}
	}
	return backoff
}

// isRetryableError determines if an error should trigger retry/failover logic
func (cm *Manager) isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for connection-related errors that justify failover
	errStr := err.Error()
	retryablePatterns := []string{
		"connection refused",
		"no servers available",
		"connection closed",
		"broken pipe",
		"connection reset",
		"timeout",
		"network is unreachable",
		"no route to host",
		"connection timed out",
		nats.ErrConnectionClosed.Error(),
		nats.ErrConnectionDraining.Error(),
	}

	for _, pattern := range retryablePatterns {
		if utils.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// NATS event handlers

// handleDisconnect is called when the connection is disconnected
func (cm *Manager) handleDisconnect(conn *nats.Conn, err error) {
	if err != nil {
		cm.logger.Warning("NATS connection disconnected: %v", err)
	} else {
		cm.logger.Info("NATS connection disconnected")
	}
}

// handleReconnect is called when the connection is reconnected
func (cm *Manager) handleReconnect(conn *nats.Conn) {
	cm.logger.Success("NATS connection reconnected to: %s", conn.ConnectedUrl())
}

// handleClosed is called when the connection is closed
func (cm *Manager) handleClosed(conn *nats.Conn) {
	cm.logger.Info("NATS connection closed")
}
