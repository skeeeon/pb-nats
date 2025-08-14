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
*NATS accounts providing isolation boundaries*

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
*Permission templates*

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `publish_permissions` | Text | JSON array of publish subjects |
| `subscribe_permissions` | Text | JSON array of subscribe subjects |
| `max_connections` | Number | Connection limit (-1 = unlimited) |
| `max_data` | Number | Data limit (-1 = unlimited) |
| `max_payload` | Number | Payload limit (-1 = unlimited) |

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

### IoT Platform
- Account per customer/building
- Device users with sensor permissions  
- Real-time telemetry streaming
- Emergency account rotation for compromised customers

### Multi-Tenant SaaS
- Account per tenant for data isolation
- Role-based permissions within accounts
- Cross-tenant communication via exports/imports
- Account-level security operations

### Development Teams
- Account per team (frontend, backend, devops)
- Isolated team communication channels
- CI/CD service accounts
- Team-level incident response

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

## 📄 License

MIT License - see LICENSE file for details.
