# PocketBase NATS JWT Authentication

A high-performance library for seamless integration between [PocketBase](https://pocketbase.io/) and [NATS Server](https://nats.io/) using JWT-based authentication. This library automatically generates and manages NATS JWTs in real-time, eliminating traditional configuration file management.

## 🚀 Key Features

- **⚡ High-Performance JWT Generation**: Sub-millisecond operations using pure Go libraries
- **🔄 Real-time Synchronization**: Direct JWT publishing to NATS via `$SYS.REQ.CLAIMS.UPDATE`
- **🏢 Account-Based Architecture**: Natural isolation boundaries without subject scoping
- **🥾 Graceful Bootstrap**: Starts without NATS, connects when available (solves chicken-and-egg problem)
- **🔗 Persistent Connections**: Single connection with automatic failover and intelligent failback
- **🔒 Security Features**: Account signing key rotation with immediate user JWT invalidation
- **⚡ Queue-Based Publishing**: Reliable operations with retry logic and automatic cleanup
- **🔑 Simple Regeneration**: JWT refresh via boolean field triggers
- **📊 Account Limits**: Configurable resource limits at both account and user levels
- **⚙️ Hierarchical Limits**: Account-level limits control overall resources, role-based limits control per-user resources

## 📦 Installation

```bash
go get github.com/skeeeon/pb-nats
```

## 🚀 Quick Start

```go
package main

import (
    "log"
    "github.com/pocketbase/pocketbase"
    "github.com/skeeeon/pb-nats"
)

func main() {
    app := pocketbase.New()
    
    // Setup with defaults - starts even without NATS running
    if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup NATS sync: %v", err)
    }
    
    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

## 🥾 Bootstrap Process

**Problem**: Need operator JWT to configure NATS, but pb-nats needs NATS running.

**Solution**: Graceful bootstrap mode:

1. **Start PocketBase** (works without NATS)
2. **Extract operator JWT** from admin interface: Collections → `nats_system_operator`
3. **Configure NATS** with operator JWT in `nats.conf`
4. **Start NATS** - pb-nats automatically connects and processes queued operations

## 📋 Collections Schema

### System Operator (`nats_system_operator`)
*Contains operator JWT for NATS server configuration*

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Operator name |
| `public_key` | Text | Operator public key |
| `private_key` | Text | Operator private key |
| `seed` | Text | Operator seed |
| `signing_public_key` | Text | Signing public key |
| `signing_private_key` | Text | Signing private key |
| `signing_seed` | Text | Signing seed |
| `jwt` | Text | **Operator JWT for NATS configuration** |
| `created` | DateTime | Creation timestamp |
| `updated` | DateTime | Update timestamp |

### Accounts (`nats_accounts`)
*NATS accounts providing isolation boundaries with configurable limits*

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Account display name |
| `description` | Text | Account description |
| `public_key` | Text | Account public key |
| `private_key` | Text | Account private key |
| `seed` | Text | Account seed |
| `signing_public_key` | Text | Account signing public key |
| `signing_private_key` | Text | Account signing private key |
| `signing_seed` | Text | Account signing seed |
| `jwt` | Text | Account JWT |
| `active` | Boolean | Account status |
| `rotate_keys` | Boolean | **Triggers signing key rotation** |
| `max_connections` | Number | **Max concurrent connections** (-1 = unlimited, 0 = disabled) |
| `max_subscriptions` | Number | **Max subscriptions across account** (-1 = unlimited, 0 = disabled) |
| `max_data` | Number | **Max bytes in-flight across account** (-1 = unlimited, 0 = disabled) |
| `max_payload` | Number | **Max message size for account** (-1 = unlimited, 0 = disabled) |
| `max_jetstream_disk_storage` | Number | **Max JetStream disk storage** (-1 = unlimited, 0 = disabled) |
| `max_jetstream_memory_storage` | Number | **Max JetStream memory storage** (-1 = unlimited, 0 = disabled) |
| `created` | DateTime | Creation timestamp |
| `updated` | DateTime | Update timestamp |

### Users (`nats_users`) 
*PocketBase auth collection with NATS integration*

| Field | Type | Description |
|-------|------|-------------|
| `email` | Email | PocketBase email |
| `password` | Password | PocketBase password |
| `verified` | Boolean | Email verification status |
| `nats_username` | Text | NATS username |
| `description` | Text | User description |
| `account_id` | Relation | Link to account |
| `role_id` | Relation | Link to role |
| `public_key` | Text | User public key |
| `private_key` | Text | User private key |
| `seed` | Text | User seed |
| `jwt` | Text | User JWT |
| `creds_file` | Text | Complete .creds file |
| `regenerate` | Boolean | **Triggers JWT regeneration** |
| `active` | Boolean | User status |

### Roles (`nats_roles`)
*Permission templates with per-user limits*

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `publish_permissions` | Text | JSON array of publish subjects |
| `subscribe_permissions` | Text | JSON array of subscribe subjects |
| `max_subscriptions` | Number | **Max subscriptions per user** (-1 = unlimited, 0 = disabled) |
| `max_data` | Number | **Data limit per user** (-1 = unlimited, 0 = disabled) |
| `max_payload` | Number | **Message size limit per user** (-1 = unlimited, 0 = disabled) |

## 📊 Resource Limits Hierarchy

pb-nats implements a two-tier resource limit system:

### Account-Level Limits (Shared Resources)
Set on the **account** record, these limits control total resources across the entire account:
- `max_connections`: Total concurrent connections to the account
- `max_subscriptions`: Total subscriptions across all users in account
- `max_data`: Total bytes in-flight across all users in account
- `max_payload`: Maximum message size for the account
- `max_jetstream_disk_storage`: Total JetStream disk usage for account
- `max_jetstream_memory_storage`: Total JetStream memory usage for account

### User-Level Limits (Per-User Resources)
Set on the **role** record, these limits control individual user resource usage:
- `max_subscriptions`: Maximum concurrent subscriptions per user
- `max_data`: Maximum bytes a single user can have in-flight
- `max_payload`: Maximum message size a single user can send

### Limit Values ⚠️ IMPORTANT
**FIXED**: Corrected NATS semantics for limit values:

- **`-1`**: **Unlimited** (no restrictions)
- **`0`**: **Disabled** (no access allowed - **use with caution**)
- **`positive`**: **Specific limits** in appropriate units (bytes, count, etc.)

⚠️ **Critical Warning**: Setting limits to `0` **completely disables** access for that resource. This blocks users from connecting, subscribing, or sending data entirely. Most production systems should use either `-1` (unlimited) or positive values for specific limits.

**Examples:**
- `max_connections: -1` → Unlimited connections ✅
- `max_connections: 100` → Maximum 100 connections ✅  
- `max_connections: 0` → **No connections allowed (blocked)** ⚠️

## ⚙️ Configuration Options

```go
options := pbnats.DefaultOptions()

// Collection Names
options.UserCollectionName = "nats_users"              // Default
options.RoleCollectionName = "nats_roles"              // Default  
options.AccountCollectionName = "nats_accounts"        // Default

// NATS Configuration
options.NATSServerURL = "nats://localhost:4222"        // Primary server
options.BackupNATSServerURLs = []string{               // Backup servers
    "nats://backup1:4222",
    "nats://backup2:4222", 
}
options.OperatorName = "stone-age.io"                  // Default

// Connection Management
options.ConnectionRetryConfig = &pbtypes.RetryConfig{
    MaxPrimaryRetries: 4,                               // Retries before failover
    InitialBackoff:    1 * time.Second,                 // Initial retry delay
    MaxBackoff:        8 * time.Second,                 // Maximum retry delay  
    BackoffMultiplier: 2.0,                             // Backoff progression
    FailbackInterval:  30 * time.Second,                // Primary health checks
}

options.ConnectionTimeouts = &pbtypes.TimeoutConfig{
    ConnectTimeout: 5 * time.Second,                    // Connection timeout
    PublishTimeout: 10 * time.Second,                   // Publish timeout
    RequestTimeout: 10 * time.Second,                   // Request timeout
}

// Performance & Cleanup
options.PublishQueueInterval = 30 * time.Second        // Queue processing
options.DebounceInterval = 3 * time.Second             // Change debouncing
options.FailedRecordCleanupInterval = 6 * time.Hour    // Cleanup frequency
options.FailedRecordRetentionTime = 24 * time.Hour     // Failed record retention

// Default Permissions (when role permissions are empty)
options.DefaultPublishPermissions = []string{">"}      // Full account access
options.DefaultSubscribePermissions = []string{">", "_INBOX.>"} // Full + inbox

// JWT Settings
options.DefaultJWTExpiry = 0                           // Never expires (default)

// Logging & Filtering
options.LogToConsole = true                            // Enable logging
options.EventFilter = func(collection, event string) bool { // Custom filtering
    return true // Process all events
}
```

## 🔒 Security Features

### Account Signing Key Rotation

Immediate response for security incidents:

```http
# Emergency rotation - invalidates ALL user JWTs in account immediately
PATCH /api/collections/nats_accounts/records/{account_id}
{"rotate_keys": true}

# Users must re-authenticate to get fresh credentials  
GET /api/collections/nats_users/records/{user_id}?fields=creds_file
Authorization: Bearer {valid_pocketbase_token}
```

**Security Model:**
```
Operator (root trust)
├── System Account (NATS management) 
│   └── System User (internal operations)
└── Regular Accounts (tenant isolation)
    └── Users (scoped permissions)
```

**When to Use Rotation:**
- Account compromise incidents
- Suspicious activity detection
- Periodic security hardening
- Before sensitive operations

## 🌐 API Usage

### User Operations
```bash
# Get user credentials
GET /api/collections/nats_users/records/{user_id}?fields=creds_file
Authorization: Bearer {user_token}

# Regenerate user JWT  
PATCH /api/collections/nats_users/records/{user_id}
{"regenerate": true}
```

### Admin Operations
```bash
# Get operator JWT for NATS configuration
GET /api/collections/nats_system_operator/records
Authorization: Bearer {admin_token}

# Emergency account rotation
PATCH /api/collections/nats_accounts/records/{account_id}  
{"rotate_keys": true}

# Set account limits (IMPORTANT: Use correct values)
PATCH /api/collections/nats_accounts/records/{account_id}
{
    "max_connections": 100,        // 100 connections max
    "max_subscriptions": 1000,     // 1000 subscriptions max
    "max_data": 1048576,           // 1MB data max
    "max_payload": 65536           // 64KB message max
}

# DANGEROUS: Disable account completely
PATCH /api/collections/nats_accounts/records/{account_id}
{
    "max_connections": 0,          // ⚠️ BLOCKS all connections!
    "max_subscriptions": 0,        // ⚠️ BLOCKS all subscriptions!
    "max_data": 0,                 // ⚠️ BLOCKS all data!
    "max_payload": 0               // ⚠️ BLOCKS all messages!
}

# Safe unlimited settings
PATCH /api/collections/nats_accounts/records/{account_id}
{
    "max_connections": -1,         // ✅ Unlimited connections
    "max_subscriptions": -1,       // ✅ Unlimited subscriptions  
    "max_data": -1,                // ✅ Unlimited data
    "max_payload": -1              // ✅ Unlimited message size
}

# List users in account
GET /api/collections/nats_users/records?filter=account_id="{account_id}"
```

### Client Connection
```javascript
const pb = new PocketBase('http://localhost:8090');
await pb.collection('users').authWithPassword('user@example.com', 'password');

const user = await pb.collection('nats_users').getOne(pb.authStore.model.id, {
    fields: 'creds_file'
});

import { connect, credsAuthenticator } from 'nats';
const nc = await connect({
    servers: ["nats://your-server:4222"],
    authenticator: credsAuthenticator(new TextEncoder().encode(user.creds_file))
});

// Simple permissions within account boundaries
await nc.publish("sensors.temperature", JSON.stringify({temp: 23.5}));
await nc.publish("alerts.high_temp", JSON.stringify({location: "server_room"}));
```

## 📊 Account Isolation

Accounts provide natural boundaries - no subject scoping needed:

```go
// Account: "company-a" 
// Users: "sensors.temperature", "alerts.critical"

// Account: "company-b"
// Users: "sensors.temperature", "alerts.critical" 
// Completely isolated from company-a

// No complex scoping: "company_a.sensors.temperature"
```

## 🏭 Production Setup

### NATS Server Configuration
```conf
# /etc/nats/nats.conf
operator: /etc/nats/operator.jwt  # From nats_system_operator collection

resolver: {
    type: full
    dir: '/etc/nats/resolver'
    allow_delete: false
    interval: "2m"
}

system_account: <SYSTEM_ACCOUNT_PUBLIC_KEY>
port: 4222
http_port: 8222
```

### High Availability Example
```go
options := pbnats.DefaultOptions()
options.NATSServerURL = "nats://nats1.company.com:4222"
options.BackupNATSServerURLs = []string{
    "nats://nats2.company.com:4222",
    "nats://nats3.company.com:4222",
}

// Quick failover settings
options.ConnectionRetryConfig.MaxPrimaryRetries = 3
options.ConnectionRetryConfig.InitialBackoff = 100 * time.Millisecond
options.ConnectionTimeouts.ConnectTimeout = 3 * time.Second

if err := pbnats.Setup(app, options); err != nil {
    log.Fatalf("Failed to setup NATS sync: %v", err)
}
```

## 🎯 Use Cases

### IoT Platform with Resource Management
- Account per customer/building with connection limits
- Device users with sensor permissions and data limits
- Real-time telemetry streaming with payload size controls
- Emergency account rotation for compromised customers

### Multi-Tenant SaaS
- Account per tenant for data isolation with resource quotas
- Role-based permissions within accounts with per-user limits
- Cross-tenant communication via exports/imports
- Account-level security and resource management operations

### Development Teams
- Account per team (frontend, backend, devops) with development resource limits
- Isolated team communication channels
- CI/CD service accounts with restricted permissions
- Team-level incident response and resource monitoring

## 🐛 Troubleshooting

**Bootstrap Issues:**
- Check system operator JWT in admin interface
- Verify NATS server config matches operator JWT
- Look for "Bootstrap successful!" in logs

**Security Operations:**
- Account rotation: Check "Signing keys rotated" logs
- User invalidation: Rotation immediately invalidates JWTs via NATS
- Recovery: Users need fresh PocketBase tokens for new credentials

**Connection Issues:**
- Enable `LogToConsole: true` for detailed debugging
- Check failover logs for backup server usage
- Verify network connectivity to NATS servers

**Performance:**
- Adjust `DebounceInterval` for change frequency
- Tune `PublishQueueInterval` for processing speed
- Monitor failed record cleanup logs

**Resource Limits:**
- **Account limits**: Check NATS server monitoring for account-level usage
- **User limits**: Individual users hitting role-based limits will see connection errors
- **Unlimited values**: Use `-1` for unlimited resources
- **Disabled values**: Use `0` to completely block access (use with caution!)
- **Specific limits**: Use positive values for exact resource limits

**Limit Troubleshooting:**
- **User can't connect**: Check if `max_connections` is set to `0` (blocked)
- **User can't subscribe**: Check if `max_subscriptions` is set to `0` (blocked)  
- **User can't send data**: Check if `max_data` or `max_payload` is set to `0` (blocked)
- **Restore access**: Change `0` values to `-1` (unlimited) or positive limits

## 📚 Examples

Check the `examples/` directory for:
- `basic/` - Simple setup with default options and bootstrap process
- `advanced/` - Custom configuration with connection management and resource limits
- `integration/` - Complete workflow demonstration with JWT regeneration and account limits

## 📄 License

MIT License - see LICENSE file for details.
