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

// FailoverState represents the current operational state of the connection manager.
// These states track the lifecycle and health of NATS connectivity.
type FailoverState int

const (
	StateHealthy FailoverState = iota // Connected to primary server, all systems normal
	StateRetrying                     // Primary failed, retrying with backoff
	StateFailedOver                   // Operating on backup server
	StateBootstrapping                // Initial state, NATS not yet available
)

// String returns human-readable representation of failover state for logging.
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

// ConnectionCredentials holds authentication data for NATS connections.
// These are typically the system user's JWT and private key seed.
type ConnectionCredentials struct {
	JWT  string // System user JWT for authentication
	Seed string // System user private key seed for signing
}

// ConnectionStatus provides current connection state for monitoring and debugging.
type ConnectionStatus struct {
	Connected     bool       // True if actively connected to NATS
	CurrentURL    string     // URL of currently connected server
	FailoverState FailoverState // Current operational state
	LastFailover  *time.Time // Time of last failover event (nil if never)
}

// Manager handles persistent NATS connections with intelligent failover and graceful bootstrap.
// This solves the chicken-and-egg problem where pb-nats needs NATS running, but NATS needs
// the operator JWT from pb-nats.
//
// BOOTSTRAP PROBLEM SOLUTION:
// 1. Manager starts in bootstrap mode (NATS not required)
// 2. PocketBase generates operator JWT
// 3. Admin extracts JWT and configures NATS
// 4. Admin starts NATS server
// 5. Manager automatically detects NATS and exits bootstrap mode
//
// FAILOVER BEHAVIOR:
// Primary → Retry with backoff → Failover to backup → Background primary health checks
type Manager struct {
	// Configuration
	primaryURL   string                 // Primary NATS server URL
	backupURLs   []string               // Backup server URLs for failover
	credentials  *ConnectionCredentials // Authentication credentials
	retryConfig  *pbtypes.RetryConfig   // Retry and backoff configuration
	timeouts     *pbtypes.TimeoutConfig // Connection timeout settings

	// Connection state
	conn       *nats.Conn // Current NATS connection (nil if disconnected)
	currentURL string     // URL of currently connected server
	mu         sync.RWMutex // Protects connection state

	// Failover state
	failoverState  FailoverState // Current operational state
	lastFailover   time.Time     // Timestamp of last failover event
	failbackTicker *time.Ticker  // Timer for primary health checks

	// Bootstrap state
	isBootstrapping      bool         // True when in bootstrap mode
	bootstrapRetryTicker *time.Ticker // Timer for bootstrap connection attempts
	
	// Context and cleanup
	ctx       context.Context    // Context for graceful shutdown
	cancelCtx context.CancelFunc // Cancels all background operations

	// Utilities
	logger *utils.Logger // Logger for consistent output
}

// NewManager creates a new connection manager with failover and bootstrap capabilities.
//
// PARAMETERS:
//   - primaryURL: Primary NATS server URL
//   - backupURLs: List of backup server URLs for failover
//   - retryConfig: Retry behavior configuration (nil uses defaults)
//   - timeouts: Connection timeout configuration (nil uses defaults)
//   - logger: Logger instance for consistent output
//
// BEHAVIOR:
// - Initializes in bootstrap mode (isBootstrapping = true)
// - Applies default configurations if nil provided
// - Creates cancellable context for graceful shutdown
//
// RETURNS:
// - Manager instance ready for bootstrap or immediate connection
//
// SIDE EFFECTS:
// - Creates context and cancellation function
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

// StartBootstrap begins the bootstrap process with background connection attempts.
// This allows pb-nats to start without NATS running, solving the bootstrap problem.
//
// BOOTSTRAP FLOW:
// 1. Store credentials for later use
// 2. Start background ticker for connection attempts
// 3. Log bootstrap mode activation
// 4. Return immediately (non-blocking)
//
// PARAMETERS:
//   - jwt: System user JWT for authentication
//   - seed: System user private key seed
//
// BEHAVIOR:
// - Updates stored credentials
// - Starts background connection attempts on timer
// - Does not block or fail if NATS unavailable
//
// RETURNS: None (always succeeds)
//
// SIDE EFFECTS:
// - Starts background goroutine for connection attempts
// - Sets isBootstrapping = true
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

// Connect attempts to establish NATS connection immediately (for known-available NATS).
// If connection fails, falls back to bootstrap mode gracefully.
//
// PARAMETERS:
//   - jwt: System user JWT for authentication
//   - seed: System user private key seed
//
// BEHAVIOR:
// - Updates stored credentials
// - Exits bootstrap mode if currently bootstrapping
// - Attempts immediate connection to NATS
// - Falls back to bootstrap mode on failure (graceful degradation)
//
// RETURNS:
// - nil on successful connection or graceful bootstrap fallback
// - Never returns error (uses bootstrap mode for fault tolerance)
//
// SIDE EFFECTS:
// - May establish NATS connection
// - May start failback monitoring if connected to backup server
// - May enter bootstrap mode on failure
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

// Close terminates the connection and stops all background operations.
// Implements graceful shutdown with proper resource cleanup.
//
// SHUTDOWN SEQUENCE:
// 1. Cancel context (signals all goroutines to exit)
// 2. Give goroutines time to clean up
// 3. Stop tickers safely
// 4. Close NATS connection
//
// BEHAVIOR:
// - Cancels context to signal shutdown
// - Waits briefly for goroutines to exit
// - Stops all background tickers safely
// - Closes NATS connection if established
//
// RETURNS:
// - nil on successful cleanup
// - error if NATS connection close fails
//
// SIDE EFFECTS:
// - Stops all background goroutines
// - Closes NATS connection
// - Cleans up all resources
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

// Publish sends data to a NATS subject with automatic failover and bootstrap handling.
//
// PARAMETERS:
//   - subject: NATS subject to publish to
//   - data: Message payload as byte slice
//
// BEHAVIOR:
// - Returns specific error if in bootstrap mode (for queue handling)
// - Attempts publish on current connection
// - Triggers failover on connection errors
// - Retries publish after successful failover
//
// RETURNS:
// - nil on successful publish
// - specific "bootstrap mode" error if NATS not available (for queue handling)
// - error if publish fails even after failover attempts
//
// SIDE EFFECTS:
// - May trigger connection failover
// - May reconnect to different NATS server
func (cm *Manager) Publish(subject string, data []byte) error {
	return cm.publishWithFailover(subject, data)
}

// Request sends a request and waits for response with automatic failover and bootstrap handling.
//
// PARAMETERS:
//   - subject: NATS subject to send request to
//   - data: Request payload as byte slice
//   - timeout: Maximum time to wait for response
//
// BEHAVIOR:
// - Returns specific error if in bootstrap mode (for queue handling)
// - Attempts request on current connection
// - Triggers failover on connection errors
// - Retries request after successful failover
//
// RETURNS:
// - Response message and nil on success
// - nil message and specific "bootstrap mode" error if NATS not available
// - nil message and error if request fails even after failover attempts
//
// SIDE EFFECTS:
// - May trigger connection failover
// - May reconnect to different NATS server
func (cm *Manager) Request(subject string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	return cm.requestWithFailover(subject, data, timeout)
}

// UpdateCredentials updates authentication credentials and reconnects if needed.
// This is called when system user JWT is regenerated.
//
// PARAMETERS:
//   - jwt: New system user JWT
//   - seed: New system user private key seed
//
// BEHAVIOR:
// - Updates stored credentials
// - If bootstrapping: attempts to exit bootstrap mode
// - If connected: reconnects with new credentials
// - Falls back to bootstrap mode on reconnection failure
//
// RETURNS:
// - nil on successful credential update (never fails due to graceful degradation)
//
// SIDE EFFECTS:
// - May establish new NATS connection
// - May close existing connection
// - May change connection state
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

// GetConnectionStatus returns current connection state for monitoring and debugging.
//
// RETURNS:
// - ConnectionStatus struct with current state information
//
// THREAD SAFETY: Safe for concurrent access
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

// IsBootstrapping returns whether the manager is currently in bootstrap mode.
//
// RETURNS:
// - true if waiting for NATS to become available
// - false if connected or attempting normal operations
//
// THREAD SAFETY: Safe for concurrent access
func (cm *Manager) IsBootstrapping() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.isBootstrapping
}

// publishWithFailover handles publish operations with failover and bootstrap awareness.
//
// BOOTSTRAP HANDLING:
// Returns specific error message for bootstrap mode so queue processor
// can distinguish between temporary unavailability and real errors.
//
// FAILOVER LOGIC:
// 1. Check if in bootstrap mode → return bootstrap error
// 2. Attempt publish on current connection
// 3. On retryable error → trigger failover
// 4. Retry publish on new connection
//
// PARAMETERS:
//   - subject: NATS subject for publish
//   - data: Message payload
//
// RETURNS:
// - nil on success
// - specific bootstrap error for queue handling
// - other errors for genuine failures
//
// SIDE EFFECTS:
// - May trigger connection failover
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

// requestWithFailover handles request operations with failover and bootstrap awareness.
// Similar to publishWithFailover but for request-response patterns.
//
// PARAMETERS:
//   - subject: NATS subject for request
//   - data: Request payload
//   - timeout: Response timeout
//
// RETURNS:
// - Response message and nil on success
// - nil and specific bootstrap error for queue handling
// - nil and other errors for genuine failures
//
// SIDE EFFECTS:
// - May trigger connection failover
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

// enterBootstrapMode transitions to bootstrap mode for graceful degradation.
// This is called when all NATS servers become unavailable.
//
// BOOTSTRAP MODE BEHAVIOR:
// - All publish/request operations return specific bootstrap errors
// - Background process attempts reconnection periodically  
// - Queue processor understands bootstrap errors and waits
//
// SIDE EFFECTS:
// - Changes state to StateBootstrapping
// - Closes existing connection
// - Stops failback monitoring
// - Starts bootstrap retry process
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

// exitBootstrapMode transitions from bootstrap mode to normal operation.
// Called when NATS connection is successfully established.
//
// SIDE EFFECTS:
// - Changes state to StateHealthy
// - Stops bootstrap retry process
// - Enables normal publish/request operations
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

// startBootstrapRetry begins background connection attempts during bootstrap mode.
// Uses longer intervals than normal failover to be less aggressive during startup.
//
// RETRY INTERVAL:
// Uses FailbackInterval from config, minimum 10 seconds to avoid spam.
//
// SIDE EFFECTS:
// - Starts background goroutine
// - Creates ticker for periodic attempts
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

// tryBootstrapConnection attempts single connection during bootstrap mode.
// Exits bootstrap mode on successful connection.
//
// BEHAVIOR:
// - Only operates if still in bootstrap mode
// - Attempts connection to any available server
// - Exits bootstrap mode on success
// - Logs at info level to avoid spam
//
// SIDE EFFECTS:
// - May establish NATS connection
// - May exit bootstrap mode
// - May start failback monitoring
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

// handleConnectionFailure manages connection failures and attempts recovery.
// Implements exponential backoff for primary server, then failover to backups.
//
// FAILURE RECOVERY STRATEGY:
// 1. If on primary: retry with exponential backoff
// 2. If retries exhausted: attempt failover to backups
// 3. If all servers fail: enter bootstrap mode (graceful degradation)
//
// PARAMETERS:
//   - err: The connection error that triggered this recovery
//
// RETURNS:
// - nil on successful recovery
// - Never returns error (uses bootstrap mode for fault tolerance)
//
// SIDE EFFECTS:
// - May change connection state
// - May reconnect to different server
// - May enter bootstrap mode
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

// attemptFailover tries to connect to backup servers or enters bootstrap mode.
//
// FAILOVER STRATEGY:
// 1. Try all backup servers
// 2. Try primary server as last resort
// 3. If all fail: enter bootstrap mode (never return error)
//
// RETURNS:
// - nil (always succeeds via graceful degradation)
//
// SIDE EFFECTS:
// - May establish new connection
// - May start failback monitoring
// - May enter bootstrap mode
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

// tryAllServers attempts connection to all configured servers in priority order.
// Priority: backup servers first, then primary (to prefer working servers).
//
// CONNECTION PRIORITY:
// Backup servers are tried first because if we're calling this function,
// the primary likely failed, so backups are more likely to succeed.
//
// PARAMETERS: None (uses stored configuration)
//
// RETURNS:
// - Connection, URL, nil on success
// - nil, "", error if all servers fail
//
// SIDE EFFECTS: None (pure connection attempt)
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

// connectToURL attempts connection to a specific NATS server URL.
//
// CONNECTION CONFIGURATION:
// - JWT authentication using stored credentials
// - Configure timeouts from manager settings
// - Enable unlimited reconnection attempts (let manager handle failover)
// - Set up event handlers for connection lifecycle
//
// PARAMETERS:
//   - url: NATS server URL to connect to
//
// RETURNS:
// - NATS connection on success
// - error if connection fails
//
// SIDE EFFECTS: None (pure connection attempt)
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

// startFailbackMonitoring begins background monitoring to return to primary server.
// This runs when connected to backup server and periodically checks primary health.
//
// FAILBACK STRATEGY:
// - Periodically test primary server connectivity
// - Switch back to primary when it becomes healthy
// - Stop monitoring when back on primary
//
// SIDE EFFECTS:
// - Starts background goroutine
// - Creates ticker for periodic health checks
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

// checkPrimaryHealth tests primary server and fails back if healthy.
// Called periodically by failback monitoring when on backup server.
//
// FAILBACK CONDITIONS:
// - Currently connected to backup server
// - Primary server responds to connection test
// - Test connection can be established successfully
//
// BEHAVIOR:
// - Only operates when failed over to backup
// - Tests primary server connectivity
// - Switches to primary on successful test
// - Stops failback monitoring when back on primary
//
// SIDE EFFECTS:
// - May switch to primary server
// - May stop failback monitoring
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

// calculateBackoff computes exponential backoff delay for connection retries.
//
// BACKOFF FORMULA:
// delay = InitialBackoff * (BackoffMultiplier ^ (attempt - 1))
// capped at MaxBackoff
//
// PARAMETERS:
//   - attempt: Retry attempt number (1-based)
//
// RETURNS:
// - Backoff duration for this attempt
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

// isRetryableError determines if a connection error justifies failover attempts.
//
// RETRYABLE ERROR PATTERNS:
// - Connection refused (server down)
// - Network unreachable (network issue)
// - Connection timeouts (temporary network issue)
// - NATS-specific connection errors
//
// PARAMETERS:
//   - err: Error to classify
//
// RETURNS:
// - true if error indicates temporary network/connection issue
// - false if error indicates permanent failure (authentication, etc.)
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

// NATS event handlers for connection lifecycle monitoring

// handleDisconnect is called when NATS connection is lost.
// Logs disconnection event for monitoring and debugging.
func (cm *Manager) handleDisconnect(conn *nats.Conn, err error) {
	if err != nil {
		cm.logger.Warning("NATS connection disconnected: %v", err)
	} else {
		cm.logger.Info("NATS connection disconnected")
	}
}

// handleReconnect is called when NATS connection is automatically restored.
// This is NATS client library's internal reconnection, separate from our failover logic.
func (cm *Manager) handleReconnect(conn *nats.Conn) {
	cm.logger.Success("NATS connection reconnected to: %s", conn.ConnectedUrl())
}

// handleClosed is called when NATS connection is permanently closed.
// Logs connection closure for monitoring and debugging.
func (cm *Manager) handleClosed(conn *nats.Conn) {
	cm.logger.Info("NATS connection closed")
}
