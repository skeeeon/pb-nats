// Package pbnats provides seamless integration between PocketBase and NATS server
// This file re-exports types from internal/types for backward compatibility
package pbnats

// This file exists for backward compatibility and documentation.
// All actual type definitions are now in internal/types to avoid circular imports.
// The main types are re-exported in nats.go for external use.

// For documentation and IDE support, here are the main types available:

// OrganizationRecord represents an organization that maps to a NATS account
// Fields: ID, Name, AccountName, Description, PublicKey, PrivateKey, Seed,
//         SigningPublicKey, SigningPrivateKey, SigningSeed, JWT, Active, Created, Updated

// NatsUserRecord represents a NATS user with PocketBase authentication  
// Fields: ID, Email, Password, Verified, NatsUsername, Description, PublicKey,
//         PrivateKey, Seed, OrganizationID, RoleID, JWT, CredsFile, BearerToken,
//         JWTExpiresAt, Active

// RoleRecord represents a NATS role with permissions
// Fields: ID, Name, PublishPermissions, SubscribePermissions, Description,
//         IsDefault, MaxConnections, MaxData, MaxPayload

// SystemOperatorRecord represents the system operator (single record)
// Fields: ID, Name, PublicKey, PrivateKey, Seed, SigningPublicKey,
//         SigningPrivateKey, SigningSeed, JWT, Created, Updated

// PublishQueueRecord represents a queued account publish operation
// Fields: ID, OrganizationID, Action, Message, Attempts, Created, Updated

// Options allows customizing the behavior of the NATS JWT synchronization
// Fields: UserCollectionName, RoleCollectionName, OrganizationCollectionName,
//         OperatorName, NATSServerURL, BackupNATSServerURLs, DefaultJWTExpiry,
//         PublishQueueInterval, DebounceInterval, LogToConsole,
//         DefaultOrgPublish, DefaultOrgSubscribe, DefaultUserPublish,
//         DefaultUserSubscribe, EventFilter

// Available methods:
// - RoleRecord.GetPublishPermissions() ([]string, error)
// - RoleRecord.GetSubscribePermissions() ([]string, error)  
// - OrganizationRecord.NormalizeAccountName() string
// - ApplyOrganizationScope(permissions []string, orgName string) []string
// - ApplyUserScope(permissions []string, orgName, username string) []string

// Usage:
//   app := pocketbase.New()
//   options := pbnats.DefaultOptions()
//   options.NATSServerURL = "nats://your-server:4222"
//   if err := pbnats.Setup(app, options); err != nil {
//       log.Fatal(err)
//   }
//   app.Start()
