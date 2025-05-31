package main

import (
	"log"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/skeeeon/pb-audit"
	"github.com/skeeeon/pb-nats"
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
	// Configure pb-nats with custom options for production
	// =========================================================
	natsOptions := pbnats.DefaultOptions()
	
	// Use custom collection names
	natsOptions.UserCollectionName = "company_nats_users"
	natsOptions.RoleCollectionName = "nats_permission_roles"
	natsOptions.OrganizationCollectionName = "business_units"
	
	// Production NATS configuration
	natsOptions.NATSServerURL = "nats://nats-cluster.company.com:4222"
	natsOptions.BackupNATSServerURLs = []string{
		"nats://nats-backup1.company.com:4222",
		"nats://nats-backup2.company.com:4222",
	}
	natsOptions.OperatorName = "company-production"
	
	// Performance tuning for production
	natsOptions.PublishQueueInterval = 15 * time.Second // More frequent processing
	natsOptions.DebounceInterval = 5 * time.Second      // Longer debounce for stability
	
	// Custom default permissions for production security
	natsOptions.DefaultOrgPublish = "{org}.events.>"     // More restrictive
	natsOptions.DefaultOrgSubscribe = []string{
		"{org}.events.>",      // Organization events
		"{org}.notifications.>", // Organization notifications  
		"_INBOX.>",            // Standard inbox
		"global.announcements.>", // Company-wide announcements
	}
	
	// More restrictive user defaults
	natsOptions.DefaultUserPublish = "{org}.user.{user}.events.>"
	natsOptions.DefaultUserSubscribe = []string{
		"{org}.user.{user}.>",  // User's personal channels
		"{org}.events.>",       // Organization events
		"_INBOX.>",             // Standard inbox
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
	
	log.Println("✅ pb-audit and pb-nats initialized successfully")
	log.Println("🏢 Custom collections:", natsOptions.OrganizationCollectionName, 
		natsOptions.UserCollectionName, natsOptions.RoleCollectionName)
	log.Println("🔗 NATS server:", natsOptions.NATSServerURL)
	log.Println("👤 Operator:", natsOptions.OperatorName)
	log.Println("⚡ Queue interval:", natsOptions.PublishQueueInterval)
	log.Println("📝 Comprehensive audit logging enabled")
	
	// Start the PocketBase app as usual
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
