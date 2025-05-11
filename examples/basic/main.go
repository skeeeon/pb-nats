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

	// Setup NATS integration with custom options
	options := pbnats.DefaultOptions()
	options.ConfigFilePath = "./nats/mqtt-auth.conf"  // Adjust to your environment
	options.BackupDirPath = "./nats/backups"          // Adjust to your environment
	options.DebounceInterval = 3 * time.Second        // Wait 3 seconds after changes before updating
	options.DefaultPublish = "PUBLIC.>"               // Default publish permission
	options.DefaultSubscribe = []string{"PUBLIC.>", "_INBOX.>"} // Default subscribe permissions

	// Initialize pb-nats
	if err := pbnats.Setup(app, options); err != nil {
		log.Fatalf("Failed to setup NATS sync: %v", err)
	}

	// Start the PocketBase server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
