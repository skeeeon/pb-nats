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
	options.DefaultOrgPublish = "{org}.>"                     // Default org publish permission  
	options.DefaultOrgSubscribe = []string{"{org}.>", "_INBOX.>"} // Default org subscribe permissions
	options.PublishQueueInterval = 30 * time.Second           // Process queue every 30 seconds

	// Initialize pb-nats
	if err := pbnats.Setup(app, options); err != nil {
		log.Fatalf("Failed to setup NATS JWT sync: %v", err)
	}

	log.Println("✅ PocketBase NATS JWT sync initialized")
	log.Println("📝 Collections will be auto-created on first startup")
	log.Println("🚀 Ready to accept requests!")

	// Start the PocketBase server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
