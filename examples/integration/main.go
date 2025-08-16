package main

import (
	"encoding/json"
	"log"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/skeeeon/pb-nats"
)

// This example demonstrates a complete integration workflow:
// 1. Setup PocketBase with NATS JWT authentication
// 2. Create an account (formerly organization)
// 3. Create roles with permissions (no scoping - accounts provide isolation)
// 4. Create users and assign them to accounts and roles
// 5. Show how JWTs are automatically generated and managed
// 6. Demonstrate JWT regeneration via the 'regenerate' field

func main() {
	log.Println("🚀 Starting PocketBase NATS JWT Integration Example")

	// Initialize PocketBase
	app := pocketbase.New()

	// Configure NATS JWT options
	options := pbnats.DefaultOptions()
	options.NATSServerURL = "nats://localhost:4222"
	options.OperatorName = "demo-company"
	options.LogToConsole = true
	options.DebounceInterval = 2 * time.Second

	// Custom default permissions for this demo
	// Note: No scoping placeholders - accounts provide isolation
	options.DefaultPublishPermissions = []string{"events.>"}
	options.DefaultSubscribePermissions = []string{
		">",                 // Full account access
		"_INBOX.>",          // Standard inbox
		"global.alerts.>",   // Global alerts (if using imports/exports)
	}

	// Setup NATS JWT integration
	if err := pbnats.Setup(app, options); err != nil {
		log.Fatalf("❌ Failed to setup NATS JWT sync: %v", err)
	}

	log.Println("✅ NATS JWT sync configured successfully")

	// Setup demo data after the app starts
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		// Give the app a moment to fully initialize
		time.Sleep(2 * time.Second)
		
		// Create demo data
		go createDemoData(app)
		
		return e.Next()
	})

	log.Println("🌐 Starting PocketBase server...")
	log.Println("📝 Demo data will be created automatically")
	log.Println("🔗 Access admin UI at: http://localhost:8090/_/")
	log.Println("📚 API available at: http://localhost:8090/api/")

	// Start the server
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

func createDemoData(app *pocketbase.PocketBase) {
	log.Println("📊 Creating demo data...")

	// Wait a bit more for collections to be fully ready
	time.Sleep(3 * time.Second)

	// Create demo account
	account, err := createDemoAccount(app)
	if err != nil {
		log.Printf("❌ Failed to create demo account: %v", err)
		return
	}
	log.Printf("✅ Created account: %s (Normalized: %s)", account.GetString("name"), account.GetString("name"))

	// Create demo roles with simple permissions (no scoping needed)
	// FIXED: Using correct NATS semantics for limits (-1 = unlimited, 0 = disabled, positive = specific limit)
	adminRole, err := createRole(app, "Administrator", 
		[]string{">"},                    // Can publish anywhere in account
		[]string{">", "global.>"},        // Can subscribe to account and global
		-1, -1, -1) // ✅ Unlimited limits (-1 = unlimited)
	if err != nil {
		log.Printf("❌ Failed to create admin role: %v", err)
		return
	}
	log.Printf("✅ Created role: %s", adminRole.GetString("name"))

	sensorRole, err := createRole(app, "Sensor Manager",
		[]string{"sensors.*.telemetry", "sensors.*.status"}, // Can publish sensor data within account
		[]string{"sensors.>", "alerts.>"},                   // Can subscribe to sensors and alerts within account
		10, 1024*1024, 1024) // ✅ Limited resources (positive values = specific limits)
	if err != nil {
		log.Printf("❌ Failed to create sensor role: %v", err)
		return
	}
	log.Printf("✅ Created role: %s", sensorRole.GetString("name"))

	analystRole, err := createRole(app, "Data Analyst",
		[]string{"reports.>"},                     // Can publish reports within account
		[]string{"sensors.>", "reports.>"},       // Can subscribe to sensors and reports within account
		5, 512*1024, 512) // ✅ Moderate limits (positive values = specific limits)
	if err != nil {
		log.Printf("❌ Failed to create analyst role: %v", err)
		return
	}
	log.Printf("✅ Created role: %s", analystRole.GetString("name"))

	// Create demo users
	adminUser, err := createUser(app, "admin@demo.com", "admin123!", 
		"admin_user", "System Administrator", account.Id, adminRole.Id)
	if err != nil {
		log.Printf("❌ Failed to create admin user: %v", err)
		return
	}
	log.Printf("✅ Created user: %s (%s)", adminUser.GetString("nats_username"), adminUser.GetString("email"))

	sensorUser, err := createUser(app, "sensor@demo.com", "sensor123!",
		"sensor_manager", "Sensor Data Manager", account.Id, sensorRole.Id)
	if err != nil {
		log.Printf("❌ Failed to create sensor user: %v", err)
		return
	}
	log.Printf("✅ Created user: %s (%s)", sensorUser.GetString("nats_username"), sensorUser.GetString("email"))

	analystUser, err := createUser(app, "analyst@demo.com", "analyst123!",
		"data_analyst", "Data Analysis Specialist", account.Id, analystRole.Id)
	if err != nil {
		log.Printf("❌ Failed to create analyst user: %v", err)
		return
	}
	log.Printf("✅ Created user: %s (%s)", analystUser.GetString("nats_username"), analystUser.GetString("email"))

	log.Println("🎉 Demo data creation completed!")
	log.Println("")
	log.Println("📋 Demo Summary:")
	log.Printf("   Account: %s", account.GetString("name"))
	log.Printf("   Users Created: 3 (admin, sensor manager, analyst)")
	log.Printf("   Roles Created: 3 (admin, sensor, analyst)")
	log.Println("")
	log.Println("🔑 Login Credentials:")
	log.Println("   admin@demo.com / admin123!")
	log.Println("   sensor@demo.com / sensor123!")
	log.Println("   analyst@demo.com / analyst123!")
	log.Println("")
	log.Println("📊 Each user has automatically generated:")
	log.Println("   - NATS public/private key pair")
	log.Println("   - Signed JWT with role-based permissions")
	log.Println("   - Complete .creds file for NATS connection")
	log.Println("   - Account-isolated subject permissions")
	log.Println("")
	log.Println("🔗 Access via API:")
	log.Println("   GET /api/collections/nats_users/records (list users)")
	log.Println("   GET /api/collections/nats_accounts/records (list accounts)")
	log.Println("   GET /api/collections/nats_roles/records (list roles)")
	log.Println("")
	log.Println("🔄 JWT Regeneration:")
	log.Println("   - Set 'regenerate' field to true on any user to regenerate JWT")
	log.Println("   - Useful for credential rotation or permission updates")
	log.Println("")
	log.Println("💡 NATS Subject Examples (within account isolation):")
	log.Println("   - sensors.temperature (simple, no scoping needed)")
	log.Println("   - alerts.high_temp")
	log.Println("   - reports.daily_summary")
	log.Println("   - user.admin_user.notifications")
	log.Println("")
	log.Println("⚠️  NATS Limit Values (IMPORTANT):")
	log.Println("   - (-1) = Unlimited resources")
	log.Println("   - (0) = DISABLED/BLOCKED (no access allowed)")
	log.Println("   - (positive) = Specific resource limits")
	log.Println("   - Admin role: unlimited (-1) for all resources")
	log.Println("   - Sensor role: 10 subscriptions, 1MB data, 1KB messages")
	log.Println("   - Analyst role: 5 subscriptions, 512KB data, 512B messages")
}

func createDemoAccount(app *pocketbase.PocketBase) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("nats_accounts")
	if err != nil {
		return nil, err
	}

	// Check if demo account already exists
	existing, err := app.FindAllRecords("nats_accounts", `name = "Demo Company"`)
	if err == nil && len(existing) > 0 {
		return existing[0], nil
	}

	record := core.NewRecord(collection)
	record.Set("name", "Demo Company")
	record.Set("description", "Demonstration account for NATS JWT integration")
	record.Set("active", true)

	// FIXED: Set account limits using correct NATS semantics
	// -1 = unlimited, 0 = disabled/blocked, positive = specific limit
	record.Set("max_connections", -1)        // ✅ Unlimited connections for demo
	record.Set("max_subscriptions", -1)      // ✅ Unlimited subscriptions for demo
	record.Set("max_data", -1)               // ✅ Unlimited data for demo
	record.Set("max_payload", -1)            // ✅ Unlimited message size for demo
	record.Set("max_jetstream_disk_storage", -1)    // ✅ Unlimited JetStream disk
	record.Set("max_jetstream_memory_storage", -1)  // ✅ Unlimited JetStream memory

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

func createRole(app *pocketbase.PocketBase, name string, publishPerms, subscribePerms []string, 
	maxSubscriptions, maxData, maxPayload int64) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("nats_roles")
	if err != nil {
		return nil, err
	}

	// Check if role already exists
	existing, err := app.FindAllRecords("nats_roles", `name = "`+name+`"`)
	if err == nil && len(existing) > 0 {
		return existing[0], nil
	}

	record := core.NewRecord(collection)
	record.Set("name", name)
	record.Set("description", "Auto-generated role for "+name)

	// Convert permissions to JSON (no scoping - account provides isolation)
	publishJSON, _ := json.Marshal(publishPerms)
	subscribeJSON, _ := json.Marshal(subscribePerms)
	
	record.Set("publish_permissions", string(publishJSON))
	record.Set("subscribe_permissions", string(subscribeJSON))
	
	// FIXED: Set per-user limits using correct NATS semantics
	// -1 = unlimited, 0 = disabled/blocked, positive = specific limit
	record.Set("max_subscriptions", maxSubscriptions) // ✅ Per-user subscription limits
	record.Set("max_data", maxData)                   // ✅ Per-user data limits  
	record.Set("max_payload", maxPayload)             // ✅ Per-user message size limits

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

func createUser(app *pocketbase.PocketBase, email, password, natsUsername, description, 
	accountID, roleID string) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("nats_users")
	if err != nil {
		return nil, err
	}

	// Check if user already exists
	existing, err := app.FindAllRecords("nats_users", `email = "`+email+`"`)
	if err == nil && len(existing) > 0 {
		return existing[0], nil
	}

	record := core.NewRecord(collection)
	record.Set("email", email)
	record.Set("password", password)
	record.Set("nats_username", natsUsername)
	record.Set("description", description)
	record.Set("account_id", accountID) // Updated field name
	record.Set("role_id", roleID)
	record.Set("regenerate", false) // New field for JWT regeneration
	record.Set("active", true)
	record.Set("verified", true) // Auto-verify for demo

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}
