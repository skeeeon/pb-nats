package main

import (
	"log"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/skeeeon/pb-nats"
)

func main() {
	// Initialize PocketBase
	app := pocketbase.New()

	// Setup NATS JWT integration with custom options
	options := pbnats.DefaultOptions()
	options.NATSServerURL = "nats://localhost:4222"           // Your NATS server
	options.OperatorName = "my-company"                       // Your operator name
	options.DebounceInterval = 3 * time.Second                // Wait 3 seconds after changes before updating
	options.DefaultAccountPublish = ">"                       // Default account publish permission (full access within account)
	options.DefaultAccountSubscribe = []string{">", "_INBOX.>"} // Default account subscribe permissions
	options.DefaultUserPublish = "user.>"                     // Default user publish permission (user-scoped within account)
	options.DefaultUserSubscribe = []string{">", "_INBOX.>"}   // Default user subscribe permissions
	options.PublishQueueInterval = 30 * time.Second           // Process queue every 30 seconds

	// Initialize pb-nats
	if err := pbnats.Setup(app, options); err != nil {
		log.Fatalf("Failed to setup NATS JWT sync: %v", err)
	}

	log.Println("✅ PocketBase NATS JWT sync initialized")
	log.Println("📝 Collections will be auto-created on first startup:")
	log.Println("   - nats_accounts (NATS accounts)")
	log.Println("   - nats_users (NATS users)")
	log.Println("   - nats_roles (NATS roles)")
	log.Println("🚀 Ready to accept requests!")
	log.Println("")
	log.Println("💡 Key Features:")
	log.Println("   - Accounts provide isolation boundaries (no scoping needed)")
	log.Println("   - Simple subject names within accounts (e.g. 'sensors.temperature')")
	log.Println("   - JWT regeneration via 'regenerate' boolean field")
	log.Println("   - Automatic NATS server synchronization")

	// Start the PocketBase server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
