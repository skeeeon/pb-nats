// Package publisher handles publishing NATS account JWTs to NATS servers
package publisher

import (
	"context"
	"fmt"
	"sync"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/connection"
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager orchestrates reliable publishing of account JWTs to NATS servers.
// This component bridges PocketBase database changes and NATS server updates,
// ensuring account definitions are synchronized even during network outages.
//
// RELIABILITY STRATEGY:
// - Queue-based operations survive NATS outages
// - Persistent connection with automatic failover
// - Bootstrap mode handles chicken-and-egg startup problem
// - Retry logic with exponential backoff
// - Failed record cleanup prevents unbounded growth
//
// PUBLISHING FLOW:
// PocketBase Change → Queue Operation → Process Queue → Publish to NATS
//
// BOOTSTRAP INTEGRATION:
// Works seamlessly with connection manager's bootstrap mode to handle
// startup scenarios where NATS is not yet available.
type Manager struct {
	app         *pocketbase.PocketBase      // PocketBase instance for database access
	options     pbtypes.Options             // Configuration options
	logger      *utils.Logger               // Logger for consistent output
	connManager *connection.Manager         // NATS connection with failover
	mu          sync.Mutex                  // Protects concurrent operations
	ctx         context.Context             // Context for graceful shutdown
	cancelCtx   context.CancelFunc          // Cancels background operations
}

// NewManager creates a new account publisher with persistent NATS connection and graceful bootstrap.
//
// INITIALIZATION:
// - Creates connection manager with failover capabilities
// - Initializes logger for consistent output
// - Sets up cancellable context for graceful shutdown
// - Does not establish NATS connection (handled by Start())
//
// PARAMETERS:
//   - app: PocketBase application instance
//   - options: Configuration including NATS URLs, timeouts, intervals
//
// RETURNS:
// - Manager instance ready for Start()
//
// SIDE EFFECTS:
// - Creates connection manager
// - Initializes background context
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	
	logger := utils.NewLogger(options.LogToConsole)
	
	// Create connection manager
	connManager := connection.NewManager(
		options.NATSServerURL,
		options.BackupNATSServerURLs,
		options.ConnectionRetryConfig,
		options.ConnectionTimeouts,
		logger,
	)
	
	return &Manager{
		app:         app,
		options:     options,
		logger:      logger,
		connManager: connManager,
		ctx:         ctx,
		cancelCtx:   cancel,
	}
}

// Start initializes the publisher with graceful bootstrap and begins queue processing.
//
// BOOTSTRAP SEQUENCE:
// 1. Initialize NATS connection in bootstrap mode (non-blocking)
// 2. Start background queue processor
// 3. Connection established when NATS becomes available
//
// BOOTSTRAP BENEFITS:
// - Publisher starts successfully even without NATS
// - Operations queue until NATS available
// - Automatic processing when connection established
//
// RETURNS:
// - nil on successful initialization
// - Never fails due to graceful bootstrap design
//
// SIDE EFFECTS:
// - Starts background queue processor goroutine
// - Attempts NATS connection (falls back to bootstrap mode)
func (p *Manager) Start() error {
	p.logger.Start("Starting NATS account publisher with graceful bootstrap...")

	// Initialize connection in bootstrap mode (won't fail if NATS is unavailable)
	if err := p.initializeBootstrapConnection(); err != nil {
		// Log warning but don't fail - this is expected during bootstrap
		p.logger.Warning("NATS connection not available during startup (bootstrap mode): %v", err)
		p.logger.Info("Publisher will continue operating - connection will be established when NATS becomes available")
	}

	// Start the queue processor as a background goroutine
	go p.processQueuePeriodically()
	
	p.logger.Success("NATS account publisher started (bootstrap mode enabled)")
	
	return nil
}

// Stop gracefully shuts down the publisher and closes NATS connection.
//
// SHUTDOWN SEQUENCE:
// 1. Cancel context (signals background processes to stop)
// 2. Close connection manager safely
// 3. Wait for background processes to exit
//
// RETURNS: Always succeeds
//
// SIDE EFFECTS:
// - Stops background queue processor
// - Closes NATS connection
// - Cancels all pending operations
func (p *Manager) Stop() {
	p.logger.Stop("Stopping NATS account publisher...")
	
	// Cancel context first to signal background processes to stop
	p.cancelCtx()
	
	// Close connection manager safely
	if p.connManager != nil {
		if err := p.connManager.Close(); err != nil {
			p.logger.Warning("Error closing connection manager: %v", err)
		}
	}
	
	p.logger.Success("NATS account publisher stopped")
}

// PublishAccount publishes an account's JWT to NATS for immediate activation.
// This is called for real-time account updates that need immediate effect.
//
// NATS PUBLISHING:
// Sends account JWT via $SYS.REQ.CLAIMS.UPDATE to NATS resolver.
// NATS server updates its account registry and begins accepting
// connections from users in this account.
//
// PARAMETERS:
//   - account: Account record containing JWT and metadata
//
// BEHAVIOR:
// - Validates account record completeness
// - Publishes JWT via persistent NATS connection
// - Handles bootstrap mode gracefully
//
// RETURNS:
// - nil on successful publish
// - specific bootstrap error if NATS unavailable (for queue handling)
// - other errors for validation or connection failures
//
// SIDE EFFECTS:
// - Updates NATS server account registry
// - May trigger connection failover
func (p *Manager) PublishAccount(account *pbtypes.AccountRecord) error {
	if account == nil {
		return utils.WrapError(fmt.Errorf("account record is nil"), "publish account validation failed")
	}

	if err := p.validateAccount(account); err != nil {
		return utils.WrapErrorf(err, "invalid account %s", account.ID)
	}

	return p.publishAccountJWT(account.JWT, account.NormalizeName())
}

// RemoveAccount removes an account from NATS server registry.
// This prevents new connections and invalidates existing connections for the account.
//
// REMOVAL PROCESS:
// 1. Create deletion claim signed by operator
// 2. Send via $SYS.REQ.CLAIMS.DELETE to NATS
// 3. NATS removes account from resolver
//
// PARAMETERS:
//   - account: Account record to remove from NATS
//
// BEHAVIOR:
// - Creates operator-signed deletion claim
// - Sends deletion request to NATS
// - Handles bootstrap mode gracefully
//
// RETURNS:
// - nil on successful removal
// - specific bootstrap error if NATS unavailable (for queue handling)
// - other errors for validation or connection failures
//
// SIDE EFFECTS:
// - Removes account from NATS server registry
// - Disconnects all users in the account
// - May trigger connection failover
func (p *Manager) RemoveAccount(account *pbtypes.AccountRecord) error {
	if account == nil {
		return utils.WrapError(fmt.Errorf("account record is nil"), "remove account validation failed")
	}

	if err := p.validateAccount(account); err != nil {
		return utils.WrapErrorf(err, "invalid account %s", account.ID)
	}

	return p.removeAccountJWT(account.PublicKey, account.NormalizeName())
}

// QueueAccountUpdate adds an account operation to the reliable publish queue.
// This ensures operations survive NATS outages and are processed when connectivity restored.
//
// QUEUE BENEFITS:
// - Operations survive NATS outages
// - Deduplication prevents duplicate operations
// - Retry logic handles temporary failures
// - Bootstrap mode integration
//
// DEDUPLICATION STRATEGY:
// If an operation for the same account already queued (not failed):
// - Update existing operation with new action
// - Reset retry counter
// - Prevents queue bloat during rapid changes
//
// PARAMETERS:
//   - accountID: Database ID of account to operate on
//   - action: Operation type (PublishActionUpsert or PublishActionDelete)
//
// RETURNS:
// - nil on successful queueing
// - error if validation fails or database error
//
// SIDE EFFECTS:
// - Creates or updates queue record in database
// - May trigger immediate processing if queue processor ready
func (p *Manager) QueueAccountUpdate(accountID string, action string) error {
	if err := utils.ValidateRequired(accountID, "account ID"); err != nil {
		return utils.WrapError(err, "queue account update validation failed")
	}

	if action != pbtypes.PublishActionUpsert && action != pbtypes.PublishActionDelete {
		return utils.WrapErrorf(fmt.Errorf("invalid action %q, must be %s or %s", 
			action, pbtypes.PublishActionUpsert, pbtypes.PublishActionDelete), 
			"queue account update validation failed")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if there's already a queued operation for this account (excluding failed records)
	existingRecords, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName, 
		dbx.And(
			dbx.HashExp{"account_id": accountID},
			dbx.Or(
				dbx.NewExp("failed_at IS NULL"),
				dbx.HashExp{"failed_at": ""},
			),
		))
	if err != nil {
		return utils.WrapErrorf(err, "failed to check existing queue records for account %s", accountID)
	}

	if len(existingRecords) > 0 {
		// Update existing record with new action
		record := existingRecords[0]
		record.Set("action", action)
		record.Set("attempts", 0) // Reset attempts for new action
		record.Set("message", "")  // Clear previous error message
		
		if err := p.app.Save(record); err != nil {
			return utils.WrapErrorf(err, "failed to update queue record for account %s", accountID)
		}
		
		p.logger.Info("Updated queue record for account %s: action=%s", accountID, action)
	} else {
		// Create new record
		if err := p.createQueueRecord(accountID, action); err != nil {
			return utils.WrapErrorf(err, "failed to create queue record for account %s", accountID)
		}
		
		p.logger.Info("Created queue record for account %s: action=%s", accountID, action)
	}

	return nil
}

// ProcessPublishQueue processes all queued operations with bootstrap awareness and retry logic.
//
// PROCESSING STRATEGY:
// - Skip processing during bootstrap mode (NATS unavailable)
// - Process all non-failed queue records
// - Apply retry logic with attempt counting
// - Mark permanently failed records with timestamp
//
// BOOTSTRAP AWARENESS:
// During bootstrap mode, queue operations accumulate but are not processed.
// When NATS becomes available, all queued operations process automatically.
//
// RETRY LOGIC:
// - Temporary errors: increment attempt counter, retry later
// - Permanent errors: mark as failed immediately
// - Bootstrap errors: don't increment counter, wait for NATS
// - Max attempts: mark as permanently failed
//
// RETURNS:
// - nil on successful processing (individual failures logged)
// - error if queue retrieval fails
//
// SIDE EFFECTS:
// - Updates queue records with attempt counts and error messages
// - Deletes successfully processed records
// - Marks failed records with timestamps
// - May trigger NATS publishing operations
func (p *Manager) ProcessPublishQueue() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Only get non-failed records (failed_at is empty or null)
	records, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName, 
		dbx.Or(
			dbx.NewExp("failed_at IS NULL"),
			dbx.HashExp{"failed_at": ""},
		))
	if err != nil {
		return utils.WrapError(err, "failed to retrieve publish queue records")
	}

	if len(records) == 0 {
		return nil // No work to do
	}

	// Check if we're in bootstrap mode
	isBootstrapping := p.connManager.IsBootstrapping()
	if isBootstrapping {
		p.logger.Info("Bootstrap mode: %d operations queued, waiting for NATS connection", len(records))
		return nil // Don't process queue during bootstrap
	}

	p.logger.Process("Processing %d queued publish operations...", len(records))

	processed := 0
	failed := 0

	for _, record := range records {
		if err := p.processQueueRecord(record); err != nil {
			failed++
			p.logger.Warning("Failed to process queue record %s: %v", record.Id, err)
		} else {
			processed++
		}
	}

	if processed > 0 || failed > 0 {
		p.logger.Success("Queue processing complete: %d processed, %d failed", processed, failed)
	}

	return nil
}

// CleanupFailedRecords removes old failed records to prevent unbounded queue growth.
//
// CLEANUP STRATEGY:
// - Remove records marked as failed before retention cutoff
// - Preserve recent failures for debugging
// - Log cleanup statistics for monitoring
//
// RETENTION POLICY:
// Failed records older than FailedRecordRetentionTime are eligible for deletion.
// This provides debugging window while preventing database bloat.
//
// RETURNS:
// - nil on successful cleanup
// - error if cleanup operation fails
//
// SIDE EFFECTS:
// - Deletes old failed queue records
// - Logs cleanup statistics
func (p *Manager) CleanupFailedRecords() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoffTime := time.Now().Add(-p.options.FailedRecordRetentionTime)
	
	// Find old failed records using dbx expressions (consistent with existing patterns)
	failedRecords, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName,
		dbx.And(
			dbx.NewExp("failed_at IS NOT NULL"),
			dbx.NewExp("failed_at != ''"),
			dbx.NewExp("failed_at < {:cutoff}", dbx.Params{"cutoff": cutoffTime.Format(time.RFC3339)}),
		))
	if err != nil {
		return utils.WrapError(err, "failed to find old failed records")
	}

	if len(failedRecords) == 0 {
		return nil // Nothing to clean up
	}

	p.logger.Info("Cleaning up %d old failed queue records older than %v", 
		len(failedRecords), p.options.FailedRecordRetentionTime)

	cleaned := 0
	errors := 0

	// Delete old failed records
	for _, record := range failedRecords {
		if err := p.app.Delete(record); err != nil {
			errors++
			p.logger.Warning("Failed to delete old failed record %s: %v", record.Id, err)
		} else {
			cleaned++
		}
	}

	if errors > 0 {
		p.logger.Warning("Cleanup completed: %d cleaned, %d errors", cleaned, errors)
	} else if cleaned > 0 {
		p.logger.Success("Cleanup completed: %d old failed records removed", cleaned)
	}

	return nil
}

// initializeBootstrapConnection attempts NATS connection in bootstrap mode.
// This enables graceful startup when NATS is not yet available.
//
// BOOTSTRAP CONNECTION FLOW:
// 1. Get system user credentials for NATS authentication
// 2. Start connection manager in bootstrap mode
// 3. Attempt immediate connection (fails gracefully)
// 4. Connection manager continues trying in background
//
// RETURNS:
// - error message if connection fails (expected during bootstrap)
// - nil if immediate connection successful
//
// SIDE EFFECTS:
// - Starts background connection attempts
// - May establish immediate NATS connection
func (p *Manager) initializeBootstrapConnection() error {
	// Get system user for connection
	sysUser, err := p.getSystemUser()
	if err != nil {
		return utils.WrapError(err, "failed to get system user for NATS connection")
	}

	// Start in bootstrap mode - won't fail if NATS is unavailable
	p.connManager.StartBootstrap(sysUser.JWT, sysUser.Seed)

	// Try immediate connection - if it fails, bootstrap mode handles it gracefully
	err = p.connManager.Connect(sysUser.JWT, sysUser.Seed)
	if err != nil {
		// This is expected during bootstrap - connection manager will keep retrying
		return fmt.Errorf("NATS server not available yet (this is normal during bootstrap)")
	}

	p.logger.Success("NATS connection established immediately")
	return nil
}

// processQueuePeriodically runs queue processing and cleanup on scheduled intervals.
// This background process ensures operations eventually reach NATS.
//
// DUAL TIMER STRATEGY:
// - Queue processing timer: frequent operations processing
// - Cleanup timer: periodic maintenance for failed records
//
// GRACEFUL SHUTDOWN:
// Responds to context cancellation for clean shutdown.
//
// SIDE EFFECTS:
// - Runs in background goroutine
// - Processes queue periodically
// - Cleans up old failed records
// - Stops on context cancellation
func (p *Manager) processQueuePeriodically() {
	queueTicker := time.NewTicker(p.options.PublishQueueInterval)
	cleanupTicker := time.NewTicker(p.options.FailedRecordCleanupInterval)
	defer queueTicker.Stop()
	defer cleanupTicker.Stop()

	for {
		select {
		case <-queueTicker.C:
			if err := p.ProcessPublishQueue(); err != nil {
				p.logger.Warning("Queue processing error: %v", err)
			}
			
		case <-cleanupTicker.C:
			if err := p.CleanupFailedRecords(); err != nil {
				p.logger.Warning("Failed record cleanup error: %v", err)
			}
			
		case <-p.ctx.Done():
			p.logger.Info("Queue processor shutting down...")
			return
		}
	}
}

// processQueueRecord processes a single queue record with retry logic and error handling.
//
// PROCESSING LOGIC:
// 1. Check if record exceeded max attempts → mark as permanently failed
// 2. Retrieve account record from database
// 3. Execute operation (upsert or delete)
// 4. Handle different error types:
//    - Bootstrap errors: don't increment attempts
//    - Temporary errors: increment attempts, retry later  
//    - Permanent errors: mark as failed immediately
// 5. Remove successful operations from queue
//
// BOOTSTRAP HANDLING:
// Bootstrap mode errors are treated specially - they don't count against
// retry attempts since they represent temporary NATS unavailability.
//
// PARAMETERS:
//   - record: Queue record to process
//
// RETURNS:
// - nil on successful processing (record removed from queue)
// - error on processing failure (record updated with error info)
//
// SIDE EFFECTS:
// - May publish to NATS
// - Updates or deletes queue record
// - May mark record as permanently failed
func (p *Manager) processQueueRecord(record *core.Record) error {
	accountID := record.GetString("account_id")
	action := record.GetString("action")
	attempts := record.GetInt("attempts")

	// Check if this record has exceeded max attempts
	if attempts >= pbtypes.MaxQueueAttempts {
		p.logger.Warning("Marking queue record %s as permanently failed after %d attempts", record.Id, attempts)
		
		// Mark as failed with timestamp instead of just skipping
		record.Set("failed_at", time.Now())
		record.Set("message", "Exceeded maximum retry attempts")
		
		if err := p.app.Save(record); err != nil {
			return utils.WrapError(err, "failed to mark queue record as failed")
		}
		
		return nil // Successfully handled by marking as failed
	}

	// Get account record
	account, err := p.app.FindRecordById(p.options.AccountCollectionName, accountID)
	if err != nil {
		// Account might have been deleted - remove queue record
		p.logger.Delete("Account %s not found, removing queue record", accountID)
		return p.app.Delete(record)
	}

	// Convert to account model
	accountRecord := p.recordToAccountModel(account)

	// Process based on action
	var processErr error
	switch action {
	case pbtypes.PublishActionUpsert:
		processErr = p.PublishAccount(accountRecord)
	case pbtypes.PublishActionDelete:
		processErr = p.RemoveAccount(accountRecord)
	default:
		processErr = fmt.Errorf("unknown action: %s", action)
	}

	if processErr != nil {
		// Check if this is a bootstrap-related error
		if utils.Contains(processErr.Error(), "bootstrap mode") {
			// Don't increment attempts for bootstrap mode - just wait
			record.Set("message", "Waiting for NATS connection (bootstrap mode)")
			if err := p.app.Save(record); err != nil {
				return utils.WrapError(err, "failed to update queue record with bootstrap message")
			}
			return nil // Don't count as failure during bootstrap
		}

		// Update record with error and increment attempts
		record.Set("attempts", attempts+1)
		record.Set("message", utils.TruncateString(processErr.Error(), 500))
		
		if err := p.app.Save(record); err != nil {
			return utils.WrapError(err, "failed to update queue record with error")
		}
		
		return processErr
	}

	// Success - remove from queue
	p.logger.Success("Successfully processed %s for account %s", action, accountRecord.Name)
	
	return p.app.Delete(record)
}

// publishAccountJWT sends account JWT to NATS via $SYS.REQ.CLAIMS.UPDATE.
//
// NATS CLAIMS UPDATE:
// This is the standard NATS mechanism for updating account definitions:
// - Subject: $SYS.REQ.CLAIMS.UPDATE
// - Payload: Account JWT
// - Response: Status confirmation from NATS
//
// PARAMETERS:
//   - accountJWT: Complete account JWT for NATS
//   - accountName: Account name for logging
//
// BEHAVIOR:
// - Validates parameters
// - Sends request via connection manager (handles failover)
// - Returns specific bootstrap error for queue handling
//
// RETURNS:
// - nil on successful publish
// - specific bootstrap error if NATS unavailable
// - other errors for validation or connection failures
//
// SIDE EFFECTS:
// - Updates NATS server account registry
// - May trigger connection failover
func (p *Manager) publishAccountJWT(accountJWT, accountName string) error {
	if err := utils.ValidateRequired(accountJWT, "account JWT"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}
	
	if err := utils.ValidateRequired(accountName, "account name"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}

	// Use connection manager for publishing with automatic failover and bootstrap handling
	resp, err := p.connManager.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(accountJWT), p.options.ConnectionTimeouts.RequestTimeout)
	if err != nil {
		// Enhanced error message for bootstrap mode
		if utils.Contains(err.Error(), "bootstrap mode") {
			return fmt.Errorf("NATS connection not available (bootstrap mode) - account %s will be published when connection is established", accountName)
		}
		return utils.WrapErrorf(err, "failed to publish account JWT for %s", accountName)
	}

	p.logger.Publish("Published account %s to NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))

	return nil
}

// removeAccountJWT removes account from NATS via $SYS.REQ.CLAIMS.DELETE.
//
// NATS CLAIMS DELETION:
// Creates operator-signed deletion claim and sends to NATS:
// - Subject: $SYS.REQ.CLAIMS.DELETE
// - Payload: Operator JWT containing accounts to delete
// - Response: Status confirmation from NATS
//
// DELETION CLAIM STRUCTURE:
// The deletion claim is a generic JWT signed by the operator containing
// the list of account public keys to remove from the resolver.
//
// PARAMETERS:
//   - accountPublicKey: Public key of account to remove
//   - accountName: Account name for logging
//
// RETURNS:
// - nil on successful removal
// - specific bootstrap error if NATS unavailable
// - other errors for operator lookup or connection failures
//
// SIDE EFFECTS:
// - Removes account from NATS server registry
// - Disconnects all users in the account
// - May trigger connection failover
func (p *Manager) removeAccountJWT(accountPublicKey, accountName string) error {
	if err := utils.ValidateRequired(accountPublicKey, "account public key"); err != nil {
		return utils.WrapError(err, "remove account JWT validation failed")
	}
	
	if err := utils.ValidateRequired(accountName, "account name"); err != nil {
		return utils.WrapError(err, "remove account JWT validation failed")
	}

	// Get system operator for signing deletion request
	operator, err := p.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator for account deletion")
	}

	// Create deletion claim
	claim := jwt.NewGenericClaims(operator.PublicKey)
	claim.Data["accounts"] = []string{accountPublicKey}

	// Sign with operator key
	operatorKP, err := nkeys.FromSeed([]byte(operator.Seed))
	if err != nil {
		return utils.WrapError(err, "failed to create operator key pair for deletion")
	}

	deleteJWT, err := claim.Encode(operatorKP)
	if err != nil {
		return utils.WrapError(err, "failed to encode deletion JWT")
	}

	// Send deletion request using connection manager with automatic failover and bootstrap handling
	resp, err := p.connManager.Request("$SYS.REQ.CLAIMS.DELETE", []byte(deleteJWT), p.options.ConnectionTimeouts.RequestTimeout)
	if err != nil {
		// Enhanced error message for bootstrap mode
		if utils.Contains(err.Error(), "bootstrap mode") {
			return fmt.Errorf("NATS connection not available (bootstrap mode) - account %s deletion will be processed when connection is established", accountName)
		}
		return utils.WrapErrorf(err, "failed to delete account JWT for %s", accountName)
	}

	p.logger.Delete("Removed account %s from NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))

	return nil
}

// updateConnectionCredentials updates connection manager with new system user credentials.
// Called when system user JWT is regenerated.
//
// CREDENTIAL UPDATE SCENARIOS:
// - System user JWT regenerated
// - Account signing key rotated
// - System account changes
//
// PARAMETERS: None (retrieves current system user from database)
//
// RETURNS:
// - nil on successful credential update
// - error if system user lookup fails
//
// SIDE EFFECTS:
// - May reconnect to NATS with new credentials
// - May trigger connection manager state changes
func (p *Manager) updateConnectionCredentials() error {
	sysUser, err := p.getSystemUser()
	if err != nil {
		return utils.WrapError(err, "failed to get updated system user credentials")
	}

	return p.connManager.UpdateCredentials(sysUser.JWT, sysUser.Seed)
}

// Helper methods for database operations and validation

// createQueueRecord creates a new queue record for account operation.
//
// QUEUE RECORD STRUCTURE:
// - account_id: Foreign key to account
// - action: Operation type (upsert/delete)
// - attempts: Retry counter (starts at 0)
// - message: Error message (empty initially)
// - failed_at: Failure timestamp (empty initially)
//
// PARAMETERS:
//   - accountID: Database ID of account
//   - action: Operation type
//
// RETURNS:
// - nil on successful creation
// - error if database operation fails
//
// SIDE EFFECTS:
// - Creates record in publish queue collection
func (p *Manager) createQueueRecord(accountID, action string) error {
	collection, err := p.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to find publish queue collection")
	}

	record := core.NewRecord(collection)
	record.Set("account_id", accountID)
	record.Set("action", action)
	record.Set("attempts", 0)
	record.Set("message", "")
	record.Set("failed_at", "") // Ensure failed_at is empty for new records

	if err := p.app.Save(record); err != nil {
		return utils.WrapError(err, "failed to save queue record")
	}

	return nil
}

// validateAccount validates account record for publishing operations.
//
// VALIDATION CHECKS:
// - Required fields present (ID, name, public key, JWT)
// - Account is active
// - Account name can be normalized for NATS
//
// PARAMETERS:
//   - account: Account record to validate
//
// RETURNS:
// - nil if account is valid for publishing
// - error describing validation failure
func (p *Manager) validateAccount(account *pbtypes.AccountRecord) error {
	if err := utils.ValidateRequired(account.ID, "account ID"); err != nil {
		return err
	}
	
	if err := utils.ValidateRequired(account.Name, "account name"); err != nil {
		return err
	}
	
	if err := utils.ValidateRequired(account.PublicKey, "account public key"); err != nil {
		return err
	}

	if err := utils.ValidateRequired(account.JWT, "account JWT"); err != nil {
		return err
	}

	if !account.Active {
		return fmt.Errorf("account %s is inactive", account.Name)
	}

	// Validate account name format
	accountName := account.NormalizeName()
	if accountName == "" {
		return fmt.Errorf("account %s has invalid name", account.Name)
	}

	return nil
}

// recordToAccountModel converts PocketBase record to internal account model.
// Updated to include the new account limit fields with proper int64 conversion.
//
// FIELD MAPPING:
// - Database fields → Internal model fields
// - Handles type conversions and defaults
// - Used for consistent data access patterns
//
// PARAMETERS:
//   - record: PocketBase account record
//
// RETURNS:
// - AccountRecord model with mapped fields including new account limits
func (p *Manager) recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:                record.Id,
		Name:              record.GetString("name"),
		Description:       record.GetString("description"),
		PublicKey:         record.GetString("public_key"),
		PrivateKey:        record.GetString("private_key"),
		Seed:              record.GetString("seed"),
		SigningPublicKey:  record.GetString("signing_public_key"),
		SigningPrivateKey: record.GetString("signing_private_key"),
		SigningSeed:       record.GetString("signing_seed"),
		JWT:               record.GetString("jwt"),
		Active:            record.GetBool("active"),
		
		// Account-level limits (FIXED: using int64(record.GetInt()) instead of record.GetInt64())
		MaxConnections:                int64(record.GetInt("max_connections")),
		MaxSubscriptions:              int64(record.GetInt("max_subscriptions")),
		MaxData:                       int64(record.GetInt("max_data")),
		MaxPayload:                    int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:       int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage:     int64(record.GetInt("max_jetstream_memory_storage")),
	}
}

// getSystemOperator retrieves system operator record for JWT signing operations.
//
// SYSTEM OPERATOR USAGE:
// - Required for signing account deletion claims
// - Validates critical fields before use
// - Single operator per system (index 0)
//
// RETURNS:
// - SystemOperatorRecord with validated fields
// - error if operator not found or invalid
//
// VALIDATION:
// Ensures operator has required fields for JWT operations.
func (p *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := p.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system operator records")
	}
	
	if len(records) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system operator not found - ensure system is properly initialized"), 
			"system operator lookup failed")
	}

	record := records[0]
	operator := &pbtypes.SystemOperatorRecord{
		ID:                record.Id,
		Name:              record.GetString("name"),
		PublicKey:         record.GetString("public_key"),
		PrivateKey:        record.GetString("private_key"),
		Seed:              record.GetString("seed"),
		SigningPublicKey:  record.GetString("signing_public_key"),
		SigningPrivateKey: record.GetString("signing_private_key"),
		SigningSeed:       record.GetString("signing_seed"),
		JWT:               record.GetString("jwt"),
	}

	// Enhanced validation for critical fields
	if err := utils.ValidateRequired(operator.PublicKey, "operator public key"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}
	
	if err := utils.ValidateRequired(operator.Seed, "operator seed"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}

	if err := utils.ValidateRequired(operator.SigningSeed, "operator signing seed"); err != nil {
		return nil, utils.WrapError(err, "invalid system operator")
	}

	return operator, nil
}

// getSystemUser retrieves system user record for NATS authentication.
//
// SYSTEM USER LOOKUP:
// 1. Find system account by name
// 2. Find system user in that account
// 3. Validate user has required credentials
//
// SYSTEM USER PURPOSE:
// The system user authenticates pb-nats to NATS for publishing operations.
// Must have valid JWT and seed for connection authentication.
//
// RETURNS:
// - NatsUserRecord with validated credentials
// - error if user not found or invalid
//
// VALIDATION:
// Ensures system user has JWT and seed required for NATS connection.
func (p *Manager) getSystemUser() (*pbtypes.NatsUserRecord, error) {
	// First find the system account
	sysAccountRecords, err := p.app.FindAllRecords(p.options.AccountCollectionName,
		dbx.HashExp{"name": "System Account"})
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system account")
	}
	
	if len(sysAccountRecords) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system account (SYS) not found - ensure system is properly initialized"),
			"system account lookup failed")
	}
	
	sysAccountID := sysAccountRecords[0].Id

	// Find the system user in that account
	sysUserRecords, err := p.app.FindAllRecords(p.options.UserCollectionName,
		dbx.HashExp{"nats_username": "sys", "account_id": sysAccountID})
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system user")
	}
	
	if len(sysUserRecords) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system user (sys) not found - ensure system is properly initialized"),
			"system user lookup failed")
	}

	record := sysUserRecords[0]
	user := &pbtypes.NatsUserRecord{
		ID:           record.Id,
		NatsUsername: record.GetString("nats_username"),
		PublicKey:    record.GetString("public_key"),
		PrivateKey:   record.GetString("private_key"),
		Seed:         record.GetString("seed"),
		AccountID:    record.GetString("account_id"),
		RoleID:       record.GetString("role_id"),
		JWT:          record.GetString("jwt"),
		CredsFile:    record.GetString("creds_file"),
		BearerToken:  record.GetBool("bearer_token"),
		Active:       record.GetBool("active"),
	}

	// Enhanced validation for critical fields
	if err := utils.ValidateRequired(user.JWT, "system user JWT"); err != nil {
		return nil, utils.WrapError(err, "invalid system user")
	}
	
	if err := utils.ValidateRequired(user.Seed, "system user seed"); err != nil {
		return nil, utils.WrapError(err, "invalid system user")
	}

	if !user.Active {
		return nil, utils.WrapError(fmt.Errorf("system user is inactive"), "system user validation failed")
	}

	return user, nil
}
