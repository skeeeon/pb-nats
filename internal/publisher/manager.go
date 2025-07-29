// Package publisher handles publishing NATS account JWTs to NATS servers
package publisher

import (
	"context"
	"fmt"
	"sync"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats/internal/utils"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager handles publishing account JWTs to NATS servers
type Manager struct {
	app       *pocketbase.PocketBase
	options   pbtypes.Options
	logger    *utils.Logger
	mu        sync.Mutex
	ctx       context.Context
	cancelCtx context.CancelFunc
}

// NewManager creates a new account publisher
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &Manager{
		app:       app,
		options:   options,
		logger:    utils.NewLogger(options.LogToConsole),
		ctx:       ctx,
		cancelCtx: cancel,
	}
}

// Start begins the publish queue processor
func (p *Manager) Start() error {
	p.logger.Start("Starting NATS account publisher...")

	// Start the queue processor as a background goroutine
	go p.processQueuePeriodically()
	
	p.logger.Success("NATS account publisher started with %v intervals", p.options.PublishQueueInterval)
	
	return nil
}

// Stop stops the publish queue processor
func (p *Manager) Stop() {
	p.logger.Stop("Stopping NATS account publisher...")
	
	p.cancelCtx()
	
	p.logger.Success("NATS account publisher stopped")
}

// PublishAccount publishes an account's JWT to NATS
func (p *Manager) PublishAccount(account *pbtypes.AccountRecord) error {
	if account == nil {
		return utils.WrapError(fmt.Errorf("account record is nil"), "publish account validation failed")
	}

	if err := p.validateAccount(account); err != nil {
		return utils.WrapErrorf(err, "invalid account %s", account.ID)
	}

	return p.publishAccountJWT(account.JWT, account.NormalizeName())
}

// RemoveAccount removes an account from NATS
func (p *Manager) RemoveAccount(account *pbtypes.AccountRecord) error {
	if account == nil {
		return utils.WrapError(fmt.Errorf("account record is nil"), "remove account validation failed")
	}

	if err := p.validateAccount(account); err != nil {
		return utils.WrapErrorf(err, "invalid account %s", account.ID)
	}

	return p.removeAccountJWT(account.PublicKey, account.NormalizeName())
}

// QueueAccountUpdate adds an account update to the publish queue with deduplication
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

	// Check if there's already a queued operation for this account
	existingRecords, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName, 
		dbx.HashExp{"account_id": accountID})
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

// ProcessPublishQueue processes all pending publish operations
func (p *Manager) ProcessPublishQueue() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	records, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName)
	if err != nil {
		return utils.WrapError(err, "failed to retrieve publish queue records")
	}

	if len(records) == 0 {
		return nil // No work to do
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

	p.logger.Success("Queue processing complete: %d processed, %d failed", processed, failed)

	return nil
}

// processQueuePeriodically runs the queue processor on a timer
func (p *Manager) processQueuePeriodically() {
	ticker := time.NewTicker(p.options.PublishQueueInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.ProcessPublishQueue(); err != nil {
				p.logger.Warning("Queue processing error: %v", err)
			}
		case <-p.ctx.Done():
			p.logger.Info("Queue processor shutting down...")
			return
		}
	}
}

// processQueueRecord processes a single queue record with retry logic
func (p *Manager) processQueueRecord(record *core.Record) error {
	accountID := record.GetString("account_id")
	action := record.GetString("action")
	attempts := record.GetInt("attempts")

	if attempts >= pbtypes.MaxQueueAttempts {
		p.logger.Warning("Skipping queue record %s after %d attempts", record.Id, attempts)
		return nil // Don't retry further
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

// publishAccountJWT publishes an account JWT to NATS
func (p *Manager) publishAccountJWT(accountJWT, accountName string) error {
	if err := utils.ValidateRequired(accountJWT, "account JWT"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}
	
	if err := utils.ValidateRequired(accountName, "account name"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}

	// Get system user for connection
	sysUser, err := p.getSystemUser()
	if err != nil {
		return utils.WrapError(err, "failed to get system user for NATS connection")
	}

	// Connect to NATS using system user JWT and seed with proper timeouts
	nc, err := nats.Connect(p.options.NATSServerURL,
		nats.UserJWTAndSeed(sysUser.JWT, sysUser.Seed),
		nats.Timeout(pbtypes.DefaultNATSConnectTimeout))
	if err != nil {
		return utils.WrapErrorf(err, "failed to connect to NATS server %s", p.options.NATSServerURL)
	}
	defer nc.Close()

	// Publish account JWT using NATS system request with proper timeout
	resp, err := nc.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(accountJWT), pbtypes.DefaultNATSTimeout)
	if err != nil {
		return utils.WrapErrorf(err, "failed to publish account JWT for %s", accountName)
	}

	p.logger.Publish("Published account %s to NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))

	return nil
}

// removeAccountJWT removes an account JWT from NATS
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

	// Get system user for connection
	sysUser, err := p.getSystemUser()
	if err != nil {
		return utils.WrapError(err, "failed to get system user for NATS connection")
	}

	// Connect to NATS using system user JWT and seed with proper timeouts
	nc, err := nats.Connect(p.options.NATSServerURL,
		nats.UserJWTAndSeed(sysUser.JWT, sysUser.Seed),
		nats.Timeout(pbtypes.DefaultNATSConnectTimeout))
	if err != nil {
		return utils.WrapErrorf(err, "failed to connect to NATS server %s", p.options.NATSServerURL)
	}
	defer nc.Close()

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

	// Send deletion request with proper timeout
	resp, err := nc.Request("$SYS.REQ.CLAIMS.DELETE", []byte(deleteJWT), pbtypes.DefaultNATSTimeout)
	if err != nil {
		return utils.WrapErrorf(err, "failed to delete account JWT for %s", accountName)
	}

	p.logger.Delete("Removed account %s from NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))

	return nil
}

// Helper methods

// createQueueRecord creates a new queue record
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

	if err := p.app.Save(record); err != nil {
		return utils.WrapError(err, "failed to save queue record")
	}

	return nil
}

// validateAccount validates an account record with enhanced validation
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

// recordToAccountModel converts a PocketBase record to an account model
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
	}
}

// getSystemOperator gets the system operator record with consistent error handling
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

// getSystemUser gets the system user record for NATS connections with enhanced validation
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
