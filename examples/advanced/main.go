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
	// Configure pb-nats with custom options
	// =========================================================
	natsOptions := pbnats.DefaultOptions()
	
	// Use custom collection names
	natsOptions.UserCollectionName = "clients"
	natsOptions.RoleCollectionName = "topic_permissions"
	
	// Configure file paths
	natsOptions.ConfigFilePath = "/opt/nats/auth.conf"
	natsOptions.BackupDirPath = "/opt/nats/backups"
	
	// Set debounce interval (wait time after changes before updating)
	natsOptions.DebounceInterval = 5 * time.Second
	
	// Setup NATS integration
	if err := pbnats.Setup(app, natsOptions); err != nil {
		log.Fatalf("Failed to setup NATS sync: %v", err)
	}
	
	log.Println("✅ pb-audit and pb-nats initialized successfully")
	
	// Start the PocketBase app as usual
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
