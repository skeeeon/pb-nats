// Package publisher handles publishing NATS account JWTs to NATS servers
package publisher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Manager handles publishing account JWTs to NATS servers
type Manager struct {
	app       *pocketbase.PocketBase
	options   pbtypes.Options
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
		ctx:       ctx,
		cancelCtx: cancel,
	}
}

// Start begins the publish queue processor
func (p *Manager) Start() error {
	// Start the queue processor as a background goroutine
	go p.processQueuePeriodically()
	
	if p.options.LogToConsole {
		log.Printf("NATS account publisher started")
	}
	
	return nil
}

// Stop stops the publish queue processor
func (p *Manager) Stop() {
	p.cancelCtx()
}

// PublishAccount publishes an organization's account JWT to NATS
func (p *Manager) PublishAccount(org *pbtypes.OrganizationRecord) error {
	return p.publishAccountJWT(org.JWT, org.NormalizeAccountName())
}

// RemoveAccount removes an organization's account from NATS
func (p *Manager) RemoveAccount(org *pbtypes.OrganizationRecord) error {
	return p.removeAccountJWT(org.PublicKey, org.NormalizeAccountName())
}

// QueueAccountUpdate adds an account update to the publish queue
func (p *Manager) QueueAccountUpdate(orgID string, action string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if there's already a queued operation for this organization
	existingRecords, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName, 
		dbx.HashExp{"organization_id": orgID})
	if err != nil {
		return fmt.Errorf("failed to check existing queue records: %w", err)
	}

	if len(existingRecords) > 0 {
		// Update existing record
		record := existingRecords[0]
		record.Set("action", action)
		record.Set("attempts", 0) // Reset attempts
		record.Set("message", "")  // Clear error message
		
		if err := p.app.Save(record); err != nil {
			return fmt.Errorf("failed to update queue record: %w", err)
		}
		
		if p.options.LogToConsole {
			log.Printf("Updated existing queue record for organization %s with action %s", orgID, action)
		}
	} else {
		// Create new record
		collection, err := p.app.FindCollectionByNameOrId(pbtypes.PublishQueueCollectionName)
		if err != nil {
			return fmt.Errorf("failed to find publish queue collection: %w", err)
		}

		record := core.NewRecord(collection)
		record.Set("organization_id", orgID)
		record.Set("action", action)
		record.Set("attempts", 0)
		record.Set("message", "")

		if err := p.app.Save(record); err != nil {
			return fmt.Errorf("failed to create queue record: %w", err)
		}
		
		if p.options.LogToConsole {
			log.Printf("Created new queue record for organization %s with action %s", orgID, action)
		}
	}

	return nil
}

// ProcessPublishQueue processes all pending publish operations
func (p *Manager) ProcessPublishQueue() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	records, err := p.app.FindAllRecords(pbtypes.PublishQueueCollectionName)
	if err != nil {
		return fmt.Errorf("failed to find queue records: %w", err)
	}

	for _, record := range records {
		if err := p.processQueueRecord(record); err != nil {
			if p.options.LogToConsole {
				log.Printf("Failed to process queue record %s: %v", record.Id, err)
			}
			// Continue processing other records
		}
	}

	return nil
}

// processQueuePeriodically runs the queue processor on a timer
func (p *Manager) processQueuePeriodically() {
	ticker := time.NewTicker(p.options.PublishQueueInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := p.ProcessPublishQueue(); err != nil && p.options.LogToConsole {
				log.Printf("Error processing publish queue: %v", err)
			}
		case <-p.ctx.Done():
			return
		}
	}
}

// processQueueRecord processes a single queue record
func (p *Manager) processQueueRecord(record *core.Record) error {
	orgID := record.GetString("organization_id")
	action := record.GetString("action")
	attempts := record.GetInt("attempts")

	// Skip if too many attempts
	const maxAttempts = 5
	if attempts >= maxAttempts {
		if p.options.LogToConsole {
			log.Printf("Skipping queue record %s after %d attempts", record.Id, attempts)
		}
		return nil
	}

	// Get organization record
	org, err := p.app.FindRecordById(p.options.OrganizationCollectionName, orgID)
	if err != nil {
		// Organization might have been deleted
		if p.options.LogToConsole {
			log.Printf("Organization %s not found, removing queue record", orgID)
		}
		return p.app.Delete(record)
	}

	// Convert to organization record
	orgRecord := &pbtypes.OrganizationRecord{
		ID:                org.Id,
		Name:              org.GetString("name"),
		AccountName:       org.GetString("account_name"),
		Description:       org.GetString("description"),
		PublicKey:         org.GetString("public_key"),
		PrivateKey:        org.GetString("private_key"),
		Seed:              org.GetString("seed"),
		SigningPublicKey:  org.GetString("signing_public_key"),
		SigningPrivateKey: org.GetString("signing_private_key"),
		SigningSeed:       org.GetString("signing_seed"),
		JWT:               org.GetString("jwt"),
		Active:            org.GetBool("active"),
	}

	// Process based on action
	var processErr error
	switch action {
	case pbtypes.PublishActionUpsert:
		processErr = p.PublishAccount(orgRecord)
	case pbtypes.PublishActionDelete:
		processErr = p.RemoveAccount(orgRecord)
	default:
		processErr = fmt.Errorf("unknown action: %s", action)
	}

	if processErr != nil {
		// Update record with error and increment attempts
		record.Set("attempts", attempts+1)
		record.Set("message", processErr.Error())
		return p.app.Save(record)
	}

	// Success - remove from queue
	if p.options.LogToConsole {
		log.Printf("Successfully processed %s for organization %s", action, orgRecord.Name)
	}
	return p.app.Delete(record)
}

// publishAccountJWT publishes an account JWT to NATS
func (p *Manager) publishAccountJWT(accountJWT, accountName string) error {
	// Get system user for connection (NOT system account)
	sysUser, err := p.getSystemUser()
	if err != nil {
		return fmt.Errorf("failed to get system user: %w", err)
	}

	// Connect to NATS using system user JWT and seed
	nc, err := nats.Connect(p.options.NATSServerURL,
		nats.UserJWTAndSeed(sysUser.JWT, sysUser.Seed))
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	// Publish account JWT
	resp, err := nc.Request("$SYS.REQ.CLAIMS.UPDATE", []byte(accountJWT), 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to publish account JWT: %w", err)
	}

	if p.options.LogToConsole {
		log.Printf("Published account %s to NATS: %s", accountName, string(resp.Data))
	}

	return nil
}

// removeAccountJWT removes an account JWT from NATS
func (p *Manager) removeAccountJWT(accountPublicKey, accountName string) error {
	// Get system operator for signing deletion request
	operator, err := p.getSystemOperator()
	if err != nil {
		return fmt.Errorf("failed to get system operator: %w", err)
	}

	// Get system user for connection (NOT system account)
	sysUser, err := p.getSystemUser()
	if err != nil {
		return fmt.Errorf("failed to get system user: %w", err)
	}

	// Connect to NATS using system user JWT and seed
	nc, err := nats.Connect(p.options.NATSServerURL,
		nats.UserJWTAndSeed(sysUser.JWT, sysUser.Seed))
	if err != nil {
		return fmt.Errorf("failed to connect to NATS: %w", err)
	}
	defer nc.Close()

	// Create deletion claim
	claim := jwt.NewGenericClaims(operator.PublicKey)
	claim.Data["accounts"] = []string{accountPublicKey}

	// Sign with operator key
	operatorKP, err := nkeys.FromSeed([]byte(operator.Seed))
	if err != nil {
		return fmt.Errorf("failed to create operator key pair: %w", err)
	}

	deleteJWT, err := claim.Encode(operatorKP)
	if err != nil {
		return fmt.Errorf("failed to encode deletion JWT: %w", err)
	}

	// Send deletion request
	resp, err := nc.Request("$SYS.REQ.CLAIMS.DELETE", []byte(deleteJWT), 5*time.Second)
	if err != nil {
		return fmt.Errorf("failed to delete account JWT: %w", err)
	}

	if p.options.LogToConsole {
		log.Printf("Removed account %s from NATS: %s", accountName, string(resp.Data))
	}

	return nil
}

// getSystemOperator gets the system operator record
func (p *Manager) getSystemOperator() (*pbtypes.SystemOperatorRecord, error) {
	records, err := p.app.FindAllRecords(pbtypes.SystemOperatorCollectionName)
	if err != nil {
		return nil, err
	}
	
	if len(records) == 0 {
		return nil, fmt.Errorf("system operator not found")
	}

	record := records[0]
	return &pbtypes.SystemOperatorRecord{
		ID:                record.Id,
		Name:              record.GetString("name"),
		PublicKey:         record.GetString("public_key"),
		PrivateKey:        record.GetString("private_key"),
		Seed:              record.GetString("seed"),
		SigningPublicKey:  record.GetString("signing_public_key"),
		SigningPrivateKey: record.GetString("signing_private_key"),
		SigningSeed:       record.GetString("signing_seed"),
		JWT:               record.GetString("jwt"),
	}, nil
}

// getSystemUser gets the system user record (NOT system account)
// This is the key fix - we need to use a USER to connect to NATS, not an account
func (p *Manager) getSystemUser() (*pbtypes.NatsUserRecord, error) {
	// First find the system account
	sysAccountRecords, err := p.app.FindAllRecords(p.options.OrganizationCollectionName,
		dbx.HashExp{"account_name": "SYS"})
	if err != nil {
		return nil, fmt.Errorf("failed to find system account: %w", err)
	}
	
	if len(sysAccountRecords) == 0 {
		return nil, fmt.Errorf("system account (SYS) not found")
	}
	
	sysAccountID := sysAccountRecords[0].Id

	// Now find the system user in that account
	sysUserRecords, err := p.app.FindAllRecords(p.options.UserCollectionName,
		dbx.HashExp{"nats_username": "sys", "organization_id": sysAccountID})
	if err != nil {
		return nil, fmt.Errorf("failed to find system user: %w", err)
	}
	
	if len(sysUserRecords) == 0 {
		return nil, fmt.Errorf("system user (sys) not found")
	}

	record := sysUserRecords[0]
	return &pbtypes.NatsUserRecord{
		ID:             record.Id,
		NatsUsername:   record.GetString("nats_username"),
		PublicKey:      record.GetString("public_key"),
		PrivateKey:     record.GetString("private_key"),
		Seed:           record.GetString("seed"),
		OrganizationID: record.GetString("organization_id"),
		RoleID:         record.GetString("role_id"),
		JWT:            record.GetString("jwt"),
		CredsFile:      record.GetString("creds_file"),
		BearerToken:    record.GetBool("bearer_token"),
		Active:         record.GetBool("active"),
	}, nil
}
