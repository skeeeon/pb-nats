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
type Manager struct {
	app         *pocketbase.PocketBase
	options     pbtypes.Options
	logger      *utils.Logger
	connManager *connection.Manager
	mu          sync.Mutex
	ctx         context.Context
	cancelCtx   context.CancelFunc
}

// NewManager creates a new account publisher with persistent NATS connection and graceful bootstrap.
func NewManager(app *pocketbase.PocketBase, options pbtypes.Options) *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	logger := utils.NewLogger(options.LogToConsole)
	
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
func (p *Manager) Start() error {
	p.logger.Start("Starting NATS account publisher with graceful bootstrap...")

	if err := p.initializeBootstrapConnection(); err != nil {
		p.logger.Warning("NATS connection not available during startup (bootstrap mode): %v", err)
		p.logger.Info("Publisher will continue operating - connection will be established when NATS becomes available")
	}

	go p.processQueuePeriodically()
	
	p.logger.Success("NATS account publisher started (bootstrap mode enabled)")
	return nil
}

// Stop gracefully shuts down the publisher and closes NATS connection.
func (p *Manager) Stop() {
	p.logger.Stop("Stopping NATS account publisher...")
	p.cancelCtx()
	
	if p.connManager != nil {
		if err := p.connManager.Close(); err != nil {
			p.logger.Warning("Error closing connection manager: %v", err)
		}
	}
	
	p.logger.Success("NATS account publisher stopped")
}

// PublishAccount publishes an account's JWT to NATS for immediate activation.
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
func (p *Manager) QueueAccountUpdate(accountID string, action string) error {
	if err := utils.ValidateRequired(accountID, "account ID"); err != nil {
		return utils.WrapError(err, "queue account update validation failed")
	}
	if action != pbtypes.PublishActionUpsert && action != pbtypes.PublishActionDelete {
		return utils.WrapErrorf(fmt.Errorf("invalid action %q", action), "queue account update validation failed")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

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
		record := existingRecords[0]
		record.Set("action", action)
		record.Set("attempts", 0)
		record.Set("message", "")
		
		if err := p.app.Save(record); err != nil {
			return utils.WrapErrorf(err, "failed to update queue record for account %s", accountID)
		}
		p.logger.Info("Updated queue record for account %s: action=%s", accountID, action)
	} else {
		if err := p.createQueueRecord(accountID, action); err != nil {
			return utils.WrapErrorf(err, "failed to create queue record for account %s", accountID)
		}
		p.logger.Info("Created queue record for account %s: action=%s", accountID, action)
	}

	return nil
}

// ProcessPublishQueue processes all queued operations with bootstrap awareness and retry logic.
func (p *Manager) ProcessPublishQueue() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	records, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName, 
		dbx.Or(
			dbx.NewExp("failed_at IS NULL"),
			dbx.HashExp{"failed_at": ""},
		))
	if err != nil {
		return utils.WrapError(err, "failed to retrieve publish queue records")
	}

	if len(records) == 0 {
		return nil
	}

	isBootstrapping := p.connManager.IsBootstrapping()
	if isBootstrapping {
		p.logger.Info("Bootstrap mode: %d operations queued, waiting for NATS connection", len(records))
		return nil
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
func (p *Manager) CleanupFailedRecords() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoffTime := time.Now().Add(-p.options.FailedRecordRetentionTime)
	
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
		return nil
	}

	p.logger.Info("Cleaning up %d old failed queue records", len(failedRecords))

	cleaned := 0
	for _, record := range failedRecords {
		if err := p.app.Delete(record); err != nil {
			p.logger.Warning("Failed to delete old failed record %s: %v", record.Id, err)
		} else {
			cleaned++
		}
	}

	if cleaned > 0 {
		p.logger.Success("Cleanup completed: %d old failed records removed", cleaned)
	}

	return nil
}

// initializeBootstrapConnection attempts NATS connection in bootstrap mode.
func (p *Manager) initializeBootstrapConnection() error {
	sysUser, err := p.getSystemUser()
	if err != nil {
		return utils.WrapError(err, "failed to get system user for NATS connection")
	}

	p.connManager.StartBootstrap(sysUser.JWT, sysUser.Seed)

	err = p.connManager.Connect(sysUser.JWT, sysUser.Seed)
	if err != nil {
		return fmt.Errorf("NATS server not available yet (this is normal during bootstrap)")
	}

	p.logger.Success("NATS connection established immediately")
	return nil
}

// processQueuePeriodically runs queue processing and cleanup on scheduled intervals.
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

// processQueueRecord processes a single queue record with retry logic.
func (p *Manager) processQueueRecord(record *core.Record) error {
	accountID := record.GetString("account_id")
	action := record.GetString("action")
	attempts := record.GetInt("attempts")

	if attempts >= pbtypes.MaxQueueAttempts {
		p.logger.Warning("Marking queue record %s as permanently failed after %d attempts", record.Id, attempts)
		record.Set("failed_at", time.Now())
		record.Set("message", "Exceeded maximum retry attempts")
		if err := p.app.Save(record); err != nil {
			return utils.WrapError(err, "failed to mark queue record as failed")
		}
		return nil
	}

	account, err := p.app.FindRecordById(p.options.AccountCollectionName, accountID)
	if err != nil {
		p.logger.Delete("Account %s not found, removing queue record", accountID)
		return p.app.Delete(record)
	}

	accountRecord := p.recordToAccountModel(account)

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
		if utils.Contains(processErr.Error(), "bootstrap mode") {
			record.Set("message", "Waiting for NATS connection (bootstrap mode)")
			if err := p.app.Save(record); err != nil {
				return utils.WrapError(err, "failed to update queue record with bootstrap message")
			}
			return nil
		}

		record.Set("attempts", attempts+1)
		record.Set("message", utils.TruncateString(processErr.Error(), 500))
		if err := p.app.Save(record); err != nil {
			return utils.WrapError(err, "failed to update queue record with error")
		}
		return processErr
	}

	p.logger.Success("Successfully processed %s for account %s", action, accountRecord.Name)
	return p.app.Delete(record)
}

// publishAccountJWT sends account JWT to NATS via $SYS.REQ.CLAIMS.UPDATE.
func (p *Manager) publishAccountJWT(accountJWT, accountName string) error {
	if err := utils.ValidateRequired(accountJWT, "account JWT"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}
	if err := utils.ValidateRequired(accountName, "account name"); err != nil {
		return utils.WrapError(err, "publish account JWT validation failed")
	}

	resp, err := p.connManager.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(accountJWT), p.options.ConnectionTimeouts.RequestTimeout)
	if err != nil {
		if utils.Contains(err.Error(), "bootstrap mode") {
			return fmt.Errorf("NATS connection not available (bootstrap mode) - account %s will be published when connection is established", accountName)
		}
		return utils.WrapErrorf(err, "failed to publish account JWT for %s", accountName)
	}

	p.logger.Publish("Published account %s to NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))
	return nil
}

// removeAccountJWT removes account from NATS via $SYS.REQ.CLAIMS.DELETE.
func (p *Manager) removeAccountJWT(accountPublicKey, accountName string) error {
	if err := utils.ValidateRequired(accountPublicKey, "account public key"); err != nil {
		return utils.WrapError(err, "remove account JWT validation failed")
	}
	if err := utils.ValidateRequired(accountName, "account name"); err != nil {
		return utils.WrapError(err, "remove account JWT validation failed")
	}

	operator, err := p.getSystemOperator()
	if err != nil {
		return utils.WrapError(err, "failed to get system operator for account deletion")
	}

	claim := jwt.NewGenericClaims(operator.PublicKey)
	claim.Data["accounts"] = []string{accountPublicKey}

	operatorKP, err := nkeys.FromSeed([]byte(operator.Seed))
	if err != nil {
		return utils.WrapError(err, "failed to create operator key pair for deletion")
	}

	deleteJWT, err := claim.Encode(operatorKP)
	if err != nil {
		return utils.WrapError(err, "failed to encode deletion JWT")
	}

	resp, err := p.connManager.Request("$SYS.REQ.CLAIMS.DELETE", []byte(deleteJWT), p.options.ConnectionTimeouts.RequestTimeout)
	if err != nil {
		if utils.Contains(err.Error(), "bootstrap mode") {
			return fmt.Errorf("NATS connection not available (bootstrap mode) - account %s deletion will be processed when connection is established", accountName)
		}
		return utils.WrapErrorf(err, "failed to delete account JWT for %s", accountName)
	}

	p.logger.Delete("Removed account %s from NATS: %s", accountName, utils.TruncateString(string(resp.Data), 100))
	return nil
}

// createQueueRecord creates a new queue record for account operation.
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
	record.Set("failed_at", "")

	if err := p.app.Save(record); err != nil {
		return utils.WrapError(err, "failed to save queue record")
	}

	return nil
}

// validateAccount validates account record for publishing operations.
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
	if account.NormalizeName() == "" {
		return fmt.Errorf("account %s has invalid name", account.Name)
	}
	return nil
}

// recordToAccountModel converts PocketBase record to internal account model.
func (p *Manager) recordToAccountModel(record *core.Record) *pbtypes.AccountRecord {
	return &pbtypes.AccountRecord{
		ID:                        record.Id,
		Name:                      record.GetString("name"),
		Description:               record.GetString("description"),
		PublicKey:                 record.GetString("public_key"),
		PrivateKey:                record.GetString("private_key"),
		Seed:                      record.GetString("seed"),
		SigningPublicKey:          record.GetString("signing_public_key"),
		SigningPrivateKey:         record.GetString("signing_private_key"),
		SigningSeed:               record.GetString("signing_seed"),
		JWT:                       record.GetString("jwt"),
		Active:                    record.GetBool("active"),
		MaxConnections:            int64(record.GetInt("max_connections")),
		MaxSubscriptions:          int64(record.GetInt("max_subscriptions")),
		MaxData:                   int64(record.GetInt("max_data")),
		MaxPayload:                int64(record.GetInt("max_payload")),
		MaxJetStreamDiskStorage:   int64(record.GetInt("max_jetstream_disk_storage")),
		MaxJetStreamMemoryStorage: int64(record.GetInt("max_jetstream_memory_storage")),
	}
}

// getSystemOperator retrieves system operator record for JWT signing operations.
func (p *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := p.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system operator records")
	}
	if len(records) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system operator not found"), "system operator lookup failed")
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
func (p *Manager) getSystemUser() (*pbtypes.NatsUserRecord, error) {
	sysAccountRecords, err := p.app.FindAllRecords(p.options.AccountCollectionName, dbx.HashExp{"name": "System Account"})
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system account")
	}
	if len(sysAccountRecords) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system account not found"), "system account lookup failed")
	}
	
	sysAccountID := sysAccountRecords[0].Id

	sysUserRecords, err := p.app.FindAllRecords(p.options.UserCollectionName, dbx.HashExp{"nats_username": "sys", "account_id": sysAccountID})
	if err != nil {
		return nil, utils.WrapError(err, "failed to find system user")
	}
	if len(sysUserRecords) == 0 {
		return nil, utils.WrapError(fmt.Errorf("system user not found"), "system user lookup failed")
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
