# PocketBase NATS JWT Authentication

A high-performance library for seamless integration between [PocketBase](https://pocketbase.io/) and [NATS Server](https://nats.io/) using modern JWT-based authentication. This library automatically generates and manages NATS JWTs in real-time, eliminating the need for traditional configuration files.

## 🚀 Key Features

- **⚡ High-Performance JWT Generation**: Sub-millisecond operations using pure Go libraries
- **🔄 Real-time Synchronization**: Direct JWT publishing to NATS servers via `$SYS.REQ.CLAIMS.UPDATE`
- **🏢 Account-Based Architecture**: Map PocketBase accounts to NATS accounts with built-in isolation
- **🔐 Native PocketBase Security**: Leverage built-in authentication and record-level permissions
- **📊 Role-Based Permissions**: Flexible permission system within account boundaries
- **🔧 Zero Configuration Files**: No file management, all data stored in PocketBase database
- **⚡ Queue-Based Publishing**: Reliable JWT publishing with retry logic and debouncing
- **🔄 JWT Regeneration**: Simple regeneration via boolean field trigger
- **🧹 Smart Cleanup**: Automatic cleanup of failed records with configurable retention
- **🛡️ Production Ready**: Built on battle-tested patterns with comprehensive error handling

## 📈 Performance Benefits

| Metric | Traditional Config | JWT Library |
|--------|-------------------|-------------|
| **Memory per operation** | 15-30MB | <1MB |
| **Operation speed** | 50-200ms | <1ms |
| **Concurrent operations** | Limited by OS | Thousands |
| **Startup time** | Process spawn | Immediate |
| **File management** | Complex | None |
| **Failed record handling** | Manual cleanup | Automatic |

## 🏗️ Architecture

```
Traditional: PocketBase Collections → Config File Generation → NATS Reload
pb-nats: PocketBase Collections → Direct JWT Generation → NATS Account Publishing
```

### Core Components
- **Accounts** → NATS Accounts (with built-in isolation boundaries)
- **Users** → NATS Users (with role-based permissions within accounts)  
- **Roles** → Permission templates
- **System Components** → Auto-managed operator and system account
- **Failed Record Management** → Automatic cleanup with configurable retention

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
    // Initialize PocketBase
    app := pocketbase.New()
    
    // Setup NATS JWT integration with default options
    if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup NATS sync: %v", err)
    }
    
    // Start the PocketBase app as usual
    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

## 📋 PocketBase Collection Schema

The library automatically creates and manages the following collections:

### 1. Accounts Collection (`nats_accounts`)

Maps to NATS accounts and provides account-level isolation.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Account display name (also used as NATS account name when normalized) |
| `description` | Text | Account description |
| `public_key` | Text | Account public key (auto-generated) |
| `private_key` | Text | Account private key (auto-generated) |
| `seed` | Text | Account seed (auto-generated) |
| `signing_public_key` | Text | Account signing public key (auto-generated) |
| `signing_private_key` | Text | Account signing private key (auto-generated) |
| `signing_seed` | Text | Account signing seed (auto-generated) |
| `jwt` | Text | Account JWT (auto-generated) |
| `active` | Boolean | Whether the account is active |
| `created` | DateTime | Auto-managed creation timestamp |
| `updated` | DateTime | Auto-managed update timestamp |

### 2. NATS Users Collection (`nats_users`)

PocketBase auth collection with NATS-specific fields.

| Field | Type | Description |
|-------|------|-------------|
| `email` | Email | PocketBase email (standard auth) |
| `password` | Password | PocketBase password (standard auth) |
| `verified` | Boolean | PocketBase email verification status |
| `nats_username` | Text | NATS username |
| `description` | Text | User description |
| `account_id` | Relation | Link to nats_accounts |
| `role_id` | Relation | Link to nats_roles |
| `public_key` | Text | User public key (auto-generated) |
| `private_key` | Text | User private key (auto-generated) |
| `seed` | Text | User seed (auto-generated) |
| `jwt` | Text | User JWT (auto-generated) |
| `creds_file` | Text | Complete .creds file (auto-generated) |
| `bearer_token` | Boolean | Enable bearer token authentication |
| `jwt_expires_at` | DateTime | Optional JWT expiration |
| `regenerate` | Boolean | Triggers JWT regeneration when set to true |
| `active` | Boolean | Whether the user is active |

### 3. Roles Collection (`nats_roles`)

Defines permission templates for users.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `publish_permissions` | Text | JSON array of publish subject patterns |
| `subscribe_permissions` | Text | JSON array of subscribe subject patterns |
| `is_default` | Boolean | Whether this is a default role |
| `max_connections` | Number | Max connections (-1 = unlimited) |
| `max_data` | Number | Max data limit (-1 = unlimited) |
| `max_payload` | Number | Max payload size (-1 = unlimited) |

### 4. Publish Queue Collection (`nats_publish_queue`) - Hidden

Internal queue for reliable NATS publishing with automatic cleanup.

| Field | Type | Description |
|-------|------|-------------|
| `account_id` | Relation | Link to nats_accounts |
| `action` | Select | "upsert" or "delete" |
| `message` | Text | Error message if failed |
| `attempts` | Number | Number of retry attempts |
| `failed_at` | DateTime | Timestamp when marked as permanently failed |
| `created` | DateTime | Queue record creation time |
| `updated` | DateTime | Last update time |

### Permission Examples

Permissions are simple subject patterns - **no scoping needed** since accounts provide isolation:

```json
// Simple permissions within account
{
  "publish_permissions": ["sensors.*.telemetry", "alerts.>"],
  "subscribe_permissions": ["sensors.>", "alerts.>", "_INBOX.>"]
}

// User-specific permissions within account
{
  "publish_permissions": ["user.reports.>"],
  "subscribe_permissions": [">", "_INBOX.>"]
}
```

## ⚙️ Configuration Options

```go
options := pbnats.DefaultOptions()

// Collection names (all prefixed with nats_)
options.UserCollectionName = "nats_users"
options.RoleCollectionName = "nats_roles"
options.AccountCollectionName = "nats_accounts"

// NATS configuration
options.NATSServerURL = "nats://your-server:4222"
options.OperatorName = "your-operator-name"

// Performance tuning
options.PublishQueueInterval = 30 * time.Second  // Queue processing frequency
options.DebounceInterval = 3 * time.Second       // Debounce rapid changes

// Failed record management (NEW)
options.FailedRecordCleanupInterval = 6 * time.Hour   // How often to run cleanup
options.FailedRecordRetentionTime = 24 * time.Hour    // Keep failed records for debugging

// JWT settings
options.DefaultJWTExpiry = 24 * time.Hour        // Set expiration (0 = never expires)

// Default permissions for users when role permissions are empty
// Note: Accounts provide isolation boundaries, no scoping needed
options.DefaultPublishPermissions = []string{">"}               // Full access within account
options.DefaultSubscribePermissions = []string{">", "_INBOX.>"} // Full access + inbox within account

// Custom event filtering
options.EventFilter = func(collectionName, eventType string) bool {
    // Only process certain events
    return true
}

// Apply the configuration
if err := pbnats.Setup(app, options); err != nil {
    log.Fatalf("Failed to setup NATS JWT sync: %v", err)
}
```

### Failed Record Management Configuration

The library now includes intelligent failed record management:

```go
// Conservative: Check frequently, keep longer for debugging
options.FailedRecordCleanupInterval = 1 * time.Hour     // Check every hour
options.FailedRecordRetentionTime = 7 * 24 * time.Hour  // Keep for 7 days

// Aggressive: Clean up quickly to save space
options.FailedRecordCleanupInterval = 12 * time.Hour    // Check twice daily
options.FailedRecordRetentionTime = 6 * time.Hour       // Delete after 6 hours

// Balanced (default): Good for most production use cases
options.FailedRecordCleanupInterval = 6 * time.Hour     // Check 4 times daily
options.FailedRecordRetentionTime = 24 * time.Hour      // Keep 1 day for debugging
```

## 🔒 Security Model

⚠️ **Important Security Notice**: This library stores all cryptographic keys as plaintext in the PocketBase database. This is by design for simplicity and performance, but you should secure your PocketBase database appropriately.

### Database Security Requirements

1. **Encrypt PocketBase Database**: Use database-level encryption
2. **Secure Network Access**: Restrict database access to authorized systems only  
3. **Access Controls**: Use PocketBase's built-in security rules
4. **Backup Security**: Ensure backups are encrypted and secured

### Record-Level Security

The library uses PocketBase's native security features:

```go
// Users can only access their own NATS credentials
collection.ViewRule = "@request.auth.id = id"

// Account admins can access users in their account
collection.ViewRule = `
  (@request.auth.id = id) ||
  (@request.auth.role = 'account_admin' && @request.auth.account_id = account_id) ||
  @request.auth.role = 'admin'
`
```

### Field-Level Visibility

```go
// Hide sensitive fields from unauthorized users
collection.ViewRule = `
  @request.auth.id = id ? "*" : 
  "id,nats_username,active,account_id"
`
```

## 🌐 API Usage

### Native PocketBase API

The library uses PocketBase's native API - no custom endpoints needed!

```bash
# User downloads their own credentials
GET /api/collections/nats_users/records/{user_id}?fields=creds_file
Authorization: Bearer {user_token}

# User gets their JWT only  
GET /api/collections/nats_users/records/{user_id}?fields=jwt
Authorization: Bearer {user_token}

# Admin lists all users (with proper filtering)
GET /api/collections/nats_users/records
Authorization: Bearer {admin_token}

# List users in specific account
GET /api/collections/nats_users/records?filter=account_id="{account_id}"
Authorization: Bearer {admin_token}

# Regenerate user JWT
PATCH /api/collections/nats_users/records/{user_id}
Authorization: Bearer {user_token}
Content-Type: application/json

{"regenerate": true}
```

### Client Connection Example

```javascript
// Download credentials via PocketBase API
const pb = new PocketBase('http://localhost:8090');
await pb.collection('users').authWithPassword('user@example.com', 'password');

// Get user's NATS credentials
const user = await pb.collection('nats_users').getOne(pb.authStore.model.id, {
    fields: 'creds_file'
});

// Connect to NATS
import { connect, credsAuthenticator } from 'nats';

const nc = await connect({
    servers: ["nats://your-server:4222"],
    authenticator: credsAuthenticator(new TextEncoder().encode(user.creds_file))
});

// Now you can publish/subscribe with your permissions (within your account)
await nc.publish("sensors.temperature", JSON.stringify({temp: 23.5}));
await nc.publish("alerts.high_temp", JSON.stringify({location: "server_room"}));
```

### JWT Regeneration

```javascript
// Regenerate JWT when needed (e.g., security rotation, permission updates)
await pb.collection('nats_users').update(pb.authStore.model.id, {
    regenerate: true
});

// Fetch updated credentials
const updatedUser = await pb.collection('nats_users').getOne(pb.authStore.model.id, {
    fields: 'jwt,creds_file'
});

// Reconnect with fresh credentials
```

## 🏭 Account Isolation

Accounts provide natural isolation boundaries - **no subject scoping needed**:

```go
// Account: "company-a"
// Users can simply use: "sensors.temperature", "alerts.critical"

// Account: "company-b"  
// Users can use the same subjects: "sensors.temperature", "alerts.critical"
// But they're completely isolated from company-a

// No need for: "company_a.sensors.temperature" vs "company_b.sensors.temperature"
```

## 📊 How It Works

### Main Processing Flow
1. **Collection Changes**: User creates/updates account, user, or role
2. **JWT Generation**: Library generates appropriate JWTs using pure Go libraries
3. **Queue Publishing**: Changes are queued for reliable processing with debouncing
4. **NATS Publishing**: JWTs are published directly to NATS via `$SYS.REQ.CLAIMS.UPDATE`
5. **Real-time Updates**: Users immediately get new permissions without server restarts

### Failed Record Management (NEW)
1. **Retry Logic**: Failed operations are retried up to 10 times with incremental backoff
2. **Intelligent Marking**: Records exceeding max attempts are marked with `failed_at` timestamp
3. **Efficient Processing**: Main queue ignores failed records, preventing wasted cycles
4. **Automatic Cleanup**: Old failed records are automatically removed after retention period
5. **Configurable Retention**: Keep failed records for debugging (default: 24 hours)

### Event Flow
```
PocketBase Record Change → 
Debounced Processing → 
JWT Generation → 
Queue Publishing → 
NATS Server Update → 
Immediate Permission Changes

Failed Records →
Mark with Timestamp →
Exclude from Processing →
Periodic Cleanup →
Database Optimization
```

### Initialization Order

The library carefully manages initialization to prevent race conditions:

1. **Collections Creation**: All required collections are created first
2. **System Components**: Operator, system account, role, and user are initialized
3. **JWT Generators**: Core JWT generation capabilities are set up
4. **Publisher**: Background queue processor is started
5. **Hooks**: PocketBase event hooks are registered
6. **Ready**: System is ready to process changes

## 🏭 Production Setup

### NATS Server Configuration

```conf
# /etc/nats/nats.conf
operator: /etc/nats/stone-age.io.jwt

resolver: {
    type: full
    dir: '/etc/nats/resolver'
    allow_delete: false
    interval: "2m"
}

system_account: <AUTO_GENERATED_SYS_ACCOUNT_KEY>
port: 4222
http_port: 8222
jetstream: enabled
```

### Production Monitoring

The library provides comprehensive logging for production monitoring:

```go
options := pbnats.DefaultOptions()
options.LogToConsole = true  // Enable detailed logging

// Monitor these log patterns:
// - "Queue processing complete: X processed, Y failed"
// - "Cleaning up N old failed queue records"
// - "Marked queue record as permanently failed"
// - "Successfully processed [action] for account [name]"
```

### Combined with pb-audit

The library works perfectly with pb-audit for comprehensive logging:

```go
import (
    "github.com/skeeeon/pb-audit"
    "github.com/skeeeon/pb-nats"
)

func main() {
    app := pocketbase.New()
    
    // Setup audit logging
    if err := pbaudit.Setup(app, pbaudit.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup audit: %v", err)
    }
    
    // Setup NATS JWT authentication  
    if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup NATS: %v", err)
    }
    
    app.Start()
}
```

## 🎯 Use Cases

### IoT Data Platform
```go
// Account: "smart-building-corp"
// Users: sensor managers, data analysts, operators
// Permissions: 
//   - Sensors can publish: "sensors.*.telemetry"  
//   - Analysts can subscribe: "sensors.>"
//   - Operators can publish alerts: "alerts.>"
```

### Multi-Tenant SaaS
```go
// Each tenant gets their own account
// Users isolated to their account's data streams
// Admins can access cross-account monitoring streams via imports/exports
// Real-time permission updates as subscriptions change
// Automatic cleanup of failed synchronization attempts
```

### Development Teams
```go
// Accounts: "frontend-team", "backend-team", "devops-team"  
// Each team gets isolated communication channels
// Cross-team collaboration channels with explicit permissions
// CI/CD systems get service account access
```

## 🐛 Troubleshooting

### Common Issues

**Q: JWTs not updating in NATS**
A: Check that your system user has proper permissions and NATS server is reachable. Verify the system account JWT is properly configured.

**Q: Users can't connect to NATS** 
A: Verify the user's account is active and role has appropriate permissions. Check that permissions don't include invalid subject patterns.

**Q: Permission denied errors**
A: Check that permissions are valid NATS subject patterns and don't include scoping placeholders (accounts provide isolation).

**Q: JWT regeneration not working**
A: Ensure the `regenerate` field is being set to `true` and the user has permission to update their own record.

**Q: Failed records building up in database**
A: Check your `FailedRecordCleanupInterval` and `FailedRecordRetentionTime` settings. Ensure the cleanup process is running (check logs for "Cleaning up N old failed queue records").

**Q: Queue processing seems slow**
A: Failed records are now automatically excluded from processing. If you see "Marked queue record as permanently failed" messages, those records won't slow down future processing.

### Debug Logging

```go
options := pbnats.DefaultOptions()
options.LogToConsole = true  // Enable detailed logging

// Check logs for:
// - Collection initialization  
// - System component creation
// - JWT generation
// - Queue processing
// - NATS publishing results
// - Failed record cleanup operations
```

### Error Classification

The library provides comprehensive error classification:

```go
import "github.com/skeeeon/pb-nats"

// Check if an error should be retried
if pbnats.IsTemporaryError(err) {
    // Retry operation
}

// Check error severity for logging/monitoring
severity := pbnats.GetErrorSeverity(err)
if severity == pbnats.SeverityCritical {
    // Alert operations team
}
```

### Failed Record Investigation

When troubleshooting failed records:

1. **Check the publish queue**: Look at the `nats_publish_queue` collection
2. **Review error messages**: Failed records contain detailed error information
3. **Check timing**: Failed records are kept for the retention period (default: 24 hours)
4. **Monitor cleanup**: Look for cleanup log messages in your application logs

## 📚 Examples

Check the `examples/` directory for:
- `basic/` - Simple setup with default options
- `advanced/` - Custom configuration with pb-audit integration  
- `integration/` - Complete workflow demonstration with JWT regeneration

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Add tests for new functionality
4. Ensure all tests pass
5. Submit a pull request

## 📄 License

MIT License - see LICENSE file for details.

## 🙏 Acknowledgments

- Built on the excellent [PocketBase](https://pocketbase.io/) framework
- Uses [NATS](https://nats.io/) JWT authentication
- Inspired by patterns from the nats-tower project
- Designed for the [stone-age.io](https://stone-age.io) platform

---

**Transform your PocketBase app into a high-performance NATS authentication server in minutes!** 🚀
