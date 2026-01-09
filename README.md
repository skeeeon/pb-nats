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
- **🚫 Deny Permissions**: Fine-grained access control with allow/deny subject patterns
- **📨 Response Permissions**: Request-reply pattern support with configurable limits

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
| `max_connections` | Number | Max concurrent connections (-1 = unlimited, 0 = disabled) |
| `max_subscriptions` | Number | Max subscriptions across account (-1 = unlimited, 0 = disabled) |
| `max_data` | Number | Max bytes in-flight across account (-1 = unlimited, 0 = disabled) |
| `max_payload` | Number | Max message size for account (-1 = unlimited, 0 = disabled) |
| `max_jetstream_disk_storage` | Number | Max JetStream disk storage (-1 = unlimited, 0 = disabled) |
| `max_jetstream_memory_storage` | Number | Max JetStream memory storage (-1 = unlimited, 0 = disabled) |

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
*Permission templates with per-user limits and deny permissions*

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `publish_permissions` | Text | JSON array of allowed publish subjects |
| `subscribe_permissions` | Text | JSON array of allowed subscribe subjects |
| `publish_deny_permissions` | Text | JSON array of denied publish subjects |
| `subscribe_deny_permissions` | Text | JSON array of denied subscribe subjects |
| `allow_response` | Boolean | **Enable response permissions for request-reply** |
| `allow_response_max` | Number | Max responses per request (-1 = unlimited, 0 = default/1) |
| `allow_response_ttl` | Number | Response TTL in seconds (0 = no limit) |
| `max_subscriptions` | Number | Max subscriptions per user (-1 = unlimited, 0 = disabled) |
| `max_data` | Number | Data limit per user (-1 = unlimited, 0 = disabled) |
| `max_payload` | Number | Message size limit per user (-1 = unlimited, 0 = disabled) |

## 🔐 Permission System

### Allow/Deny Permissions

pb-nats supports both allow and deny permission patterns. Deny permissions take precedence over allow permissions.

**Permission Evaluation Order (NATS semantics):**
1. Check if subject matches any Allow pattern
2. If allowed, check if subject matches any Deny pattern
3. Deny takes precedence over Allow for matching subjects

**Example Role Configuration:**
```json
{
  "name": "sensor_reader",
  "publish_permissions": ["sensors.>"],
  "subscribe_permissions": ["sensors.>", "alerts.>"],
  "publish_deny_permissions": ["sensors.internal.>"],
  "subscribe_deny_permissions": ["alerts.admin.>"]
}
```

This role:
- ✅ Can publish to `sensors.temperature`
- ❌ Cannot publish to `sensors.internal.config` (denied)
- ✅ Can subscribe to `alerts.critical`
- ❌ Cannot subscribe to `alerts.admin.notifications` (denied)

### Response Permissions (Request-Reply)

Response permissions control the ability to reply to requests in request-reply patterns.

**Fields:**
- `allow_response`: Boolean to enable/disable response permissions
- `allow_response_max`: Maximum number of responses per request
  - `-1` = Unlimited responses
  - `0` = Default (1 response)
  - `positive` = Specific limit
- `allow_response_ttl`: Time-to-live for responses in seconds
  - `0` = No expiration
  - `positive` = Expires after N seconds

**Example: Service Role with Request-Reply**
```json
{
  "name": "api_service",
  "publish_permissions": ["api.>"],
  "subscribe_permissions": ["api.requests.>"],
  "allow_response": true,
  "allow_response_max": 1,
  "allow_response_ttl": 30
}
```

**Note:** The system admin role has response permissions enabled by default with unlimited responses. For user-created roles, you must explicitly enable `allow_response`.

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

- **`-1`**: **Unlimited** (no restrictions)
- **`0`**: **Disabled** (no access allowed - **use with caution**)
- **`positive`**: **Specific limits** in appropriate units (bytes, count, etc.)

⚠️ **Critical Warning**: Setting limits to `0` **completely disables** access for that resource.

## ⚙️ Configuration Options

```go
options := pbnats.DefaultOptions()

// Collection Names
options.UserCollectionName = "nats_users"
options.RoleCollectionName = "nats_roles"  
options.AccountCollectionName = "nats_accounts"

// NATS Configuration
options.NATSServerURL = "nats://localhost:4222"
options.BackupNATSServerURLs = []string{
    "nats://backup1:4222",
    "nats://backup2:4222", 
}
options.OperatorName = "stone-age.io"

// Connection Management
options.ConnectionRetryConfig = &pbtypes.RetryConfig{
    MaxPrimaryRetries: 4,
    InitialBackoff:    1 * time.Second,
    MaxBackoff:        8 * time.Second,
    BackoffMultiplier: 2.0,
    FailbackInterval:  30 * time.Second,
}

// Performance & Cleanup
options.PublishQueueInterval = 30 * time.Second
options.DebounceInterval = 3 * time.Second
options.FailedRecordCleanupInterval = 6 * time.Hour
options.FailedRecordRetentionTime = 24 * time.Hour

// Default Permissions (when role permissions are empty)
options.DefaultPublishPermissions = []string{">"}
options.DefaultSubscribePermissions = []string{">", "_INBOX.>"}

// JWT Settings
options.DefaultJWTExpiry = 0 // Never expires (default)

// Logging
options.LogToConsole = true
```

## 🔒 Security Features

### Account Signing Key Rotation

Immediate response for security incidents:

```http
PATCH /api/collections/nats_accounts/records/{account_id}
{"rotate_keys": true}
```

All user JWTs in the account are immediately invalidated.

## 🌐 API Usage

### Creating Roles with Deny Permissions
```bash
POST /api/collections/nats_roles/records
{
    "name": "restricted_publisher",
    "publish_permissions": "[\"events.>\"]",
    "subscribe_permissions": "[\"events.>\"]",
    "publish_deny_permissions": "[\"events.admin.>\", \"events.internal.>\"]",
    "subscribe_deny_permissions": "[\"events.private.>\"]",
    "allow_response": false,
    "max_subscriptions": 100,
    "max_data": 1048576,
    "max_payload": 65536
}
```

### Creating Roles with Response Permissions
```bash
POST /api/collections/nats_roles/records
{
    "name": "api_responder",
    "publish_permissions": "[\"api.responses.>\"]",
    "subscribe_permissions": "[\"api.requests.>\"]",
    "allow_response": true,
    "allow_response_max": 5,
    "allow_response_ttl": 60
}
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
```

## 📊 Account Isolation

Accounts provide natural boundaries - no subject scoping needed:

```go
// Account: "company-a" 
// Users can use: "sensors.temperature", "alerts.critical"

// Account: "company-b"
// Users can use: "sensors.temperature", "alerts.critical" 
// Completely isolated from company-a
```

## 🐛 Troubleshooting

**Permission Issues:**
- Check if deny permissions are blocking expected access
- Verify allow permissions include required subjects
- Check response permissions for request-reply patterns

**Response Permission Issues:**
- Ensure `allow_response` is set to `true` on the role
- Check `allow_response_max` isn't set to `0` (which uses default of 1)
- Verify `allow_response_ttl` isn't expiring before response is sent

**Resource Limits:**
- Use `-1` for unlimited resources
- Use `0` with caution (completely blocks access)
- Use positive values for specific limits

## 📄 License

MIT License - see LICENSE file for details.
