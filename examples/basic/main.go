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
	options.DefaultPublishPermissions = []string{">"}         // Default publish permissions when role is empty (full access within account)
	options.DefaultSubscribePermissions = []string{">", "_INBOX.>"} // Default subscribe permissions when role is empty
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
	log.Println("🔄 Bootstrap Process:")
	log.Println("   1. PocketBase starts successfully (even without NATS)")
	log.Println("   2. System operator JWT is generated automatically")
	log.Println("   3. Extract operator JWT from: http://localhost:8090/_/")
	log.Println("   4. Configure and start your NATS server with the operator JWT")
	log.Println("   5. pb-nats will automatically connect when NATS becomes available")
	log.Println("")
	log.Println("💡 Key Features:")
	log.Println("   - Graceful bootstrap (works without NATS initially)")
	log.Println("   - Accounts provide isolation boundaries (no scoping needed)")
	log.Println("   - Simple subject names within accounts (e.g. 'sensors.temperature')")
	log.Println("   - JWT regeneration via 'regenerate' boolean field")
	log.Println("   - Persistent NATS connections with automatic failover")
	log.Println("   - Automatic NATS server synchronization")
	log.Println("")
	log.Println("🔧 Connection Management:")
	log.Println("   - Single persistent connection (no per-operation overhead)")
	log.Println("   - Automatic reconnection with intelligent retry logic")
	log.Println("   - Built-in support for backup NATS servers")
	log.Println("   - Bootstrap mode for chicken-and-egg scenarios")

	// Start the PocketBase server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
