package main

import (
	"log"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/skeeeon/pb-audit"
	"github.com/skeeeon/pb-nats"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

func main() {
	// Initialize PocketBase
	app := pocketbase.New()
	
	// =========================================================
	// Configure pb-audit with custom options
	// =========================================================
	auditOptions := pbaudit.DefaultOptions()
	
	// Setup audit logging
	if err := pbaudit.Setup(app, auditOptions); err != nil {
		log.Fatalf("Failed to setup audit logging: %v", err)
	}
	
	// =========================================================
	// Configure pb-nats with advanced connection management
	// =========================================================
	natsOptions := pbnats.DefaultOptions()
	
	// Use custom collection names
	natsOptions.UserCollectionName = "company_nats_users"
	natsOptions.RoleCollectionName = "nats_permission_roles"
	natsOptions.AccountCollectionName = "business_accounts"
	
	// Production NATS configuration with failover
	natsOptions.NATSServerURL = "nats://nats-cluster.company.com:4222"
	natsOptions.BackupNATSServerURLs = []string{
		"nats://nats-backup1.company.com:4222",
		"nats://nats-backup2.company.com:4222",
		"nats://nats-dr.company.com:4222", // Disaster recovery server
	}
	natsOptions.OperatorName = "company-production"
	
	// Advanced connection retry configuration
	natsOptions.ConnectionRetryConfig = &pbtypes.RetryConfig{
		MaxPrimaryRetries: 6,                   // More retries for production
		InitialBackoff:    500 * time.Millisecond, // Faster initial retry
		MaxBackoff:        10 * time.Second,    // Longer max backoff
		BackoffMultiplier: 1.5,                 // Gentler backoff progression
		FailbackInterval:  15 * time.Second,    // Check primary more frequently
	}
	
	// Production timeout configuration
	natsOptions.ConnectionTimeouts = &pbtypes.TimeoutConfig{
		ConnectTimeout: 10 * time.Second, // Longer connect timeout for production
		PublishTimeout: 30 * time.Second, // Longer publish timeout for large JWTs
		RequestTimeout: 30 * time.Second, // Longer request timeout for reliability
	}
	
	// Performance tuning for production
	natsOptions.PublishQueueInterval = 15 * time.Second // More frequent processing
	natsOptions.DebounceInterval = 5 * time.Second      // Longer debounce for stability
	
	// Production failed record cleanup
	natsOptions.FailedRecordCleanupInterval = 2 * time.Hour  // More frequent cleanup
	natsOptions.FailedRecordRetentionTime = 7 * 24 * time.Hour // Keep for 7 days
	
	// Custom default permissions for production security
	// Note: No scoping needed - accounts provide isolation
	natsOptions.DefaultPublishPermissions = []string{"events.>"} // More restrictive than full access
	natsOptions.DefaultSubscribePermissions = []string{
		"events.>",           // Account events
		"notifications.>",    // Account notifications  
		"_INBOX.>",           // Standard inbox
		"global.announcements.>", // Company-wide announcements (if using imports/exports)
	}
	
	// Custom event filtering for production
	natsOptions.EventFilter = func(collectionName, eventType string) bool {
		// Skip processing user deletions (handle manually)
		if eventType == pbnats.EventTypeUserDelete {
			log.Printf("User deletion detected - manual intervention required")
			return false
		}
		
		// Process all other events
		return true
	}
	
	// Setup NATS JWT integration
	if err := pbnats.Setup(app, natsOptions); err != nil {
		log.Fatalf("Failed to setup NATS JWT sync: %v", err)
	}
	
	log.Println("🏢 Custom collections:", natsOptions.AccountCollectionName, 
		natsOptions.UserCollectionName, natsOptions.RoleCollectionName)
	log.Println("🔗 Primary NATS server:", natsOptions.NATSServerURL)
	log.Printf("🔄 Backup servers: %v", natsOptions.BackupNATSServerURLs)
	log.Println("👤 Operator:", natsOptions.OperatorName)
	log.Println("⚡ Queue interval:", natsOptions.PublishQueueInterval)
	log.Println("🔧 Debounce interval:", natsOptions.DebounceInterval)
	log.Printf("   - Max primary retries: %d", natsOptions.ConnectionRetryConfig.MaxPrimaryRetries)
	log.Printf("   - Initial backoff: %v", natsOptions.ConnectionRetryConfig.InitialBackoff)
	log.Printf("   - Max backoff: %v", natsOptions.ConnectionRetryConfig.MaxBackoff)
	log.Printf("   - Failback interval: %v", natsOptions.ConnectionRetryConfig.FailbackInterval)
	log.Printf("   - Connect timeout: %v", natsOptions.ConnectionTimeouts.ConnectTimeout)
	log.Printf("   - Request timeout: %v", natsOptions.ConnectionTimeouts.RequestTimeout)
	
	// Start the PocketBase app as usual
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
