# PocketBase NATS JWT Authentication

A high-performance library for seamless integration between [PocketBase](https://pocketbase.io/) and [NATS Server](https://nats.io/) using modern JWT-based authentication. This library automatically generates and manages NATS JWTs in real-time, eliminating the need for traditional configuration files.

## 🚀 Key Features

- **⚡ High-Performance JWT Generation**: Sub-millisecond operations using pure Go libraries
- **🔄 Real-time Synchronization**: Direct JWT publishing to NATS servers via `$SYS.REQ.CLAIMS.UPDATE`
- **🏢 Account-Based Architecture**: Map PocketBase accounts to NATS accounts with built-in isolation
- **🔐 Native PocketBase Security**: Leverage built-in authentication and record-level permissions
- **📊 Role-Based Permissions**: Flexible permission system within account boundaries
- **🔧 Zero Configuration Files**: No file management, all data stored in PocketBase database
- **🔗 Persistent Connections**: Single persistent NATS connection with automatic failover and reconnection
- **🔄 Smart Failover**: Automatic backup server failover with intelligent failback to primary
- **🥾 Graceful Bootstrap**: Starts successfully even without NATS server (solves chicken-and-egg problem)
- **⚡ Queue-Based Publishing**: Reliable JWT publishing with retry logic and debouncing
- **🔄 JWT Regeneration**: Simple regeneration via boolean field trigger
- **🧹 Smart Cleanup**: Automatic cleanup of failed records with configurable retention
- **🛡️ Production Ready**: Built on battle-tested patterns with comprehensive error handling

## 🥾 Bootstrap Process 

**Problem**: You need the operator JWT from pb-nats to configure NATS, but pb-nats needs NATS to be running.

**Solution**: pb-nats includes **graceful bootstrap mode** that allows you to:

### Step-by-Step Bootstrap

```bash
# 1. Start PocketBase with pb-nats (works even without NATS running)
go run main.go

# 2. PocketBase starts successfully and generates system operator JWT
# 3. Access PocketBase admin interface
open http://localhost:8090/_/

# 4. Navigate to Collections → nats_system_operator
# 5. Copy the 'jwt' field from the operator record

# 6. Configure your NATS server with the operator JWT
# Example nats.conf:
operator: /path/to/operator.jwt  # Paste the JWT here

resolver: {
    type: full
    dir: '/path/to/resolver'
}

# 7. Start NATS server
nats-server -c nats.conf

# 8. pb-nats automatically detects NATS and establishes connection
# Check logs for: "Bootstrap successful! Connected to NATS server"
```

### Bootstrap Mode Features

- **✅ Starts Successfully**: PocketBase starts even if NATS is unavailable
- **🔄 Background Retry**: Continuously attempts connection when NATS becomes available
- **📊 Queue Operations**: All operations are queued until connection is established
- **🎯 Automatic Detection**: Seamlessly transitions to normal operation when NATS is ready
- **📝 Clear Logging**: Distinct log messages for bootstrap vs operational state

## 📈 Performance Benefits

| Metric | Traditional Config | JWT Library |
|--------|-------------------|-------------|
| **Memory per operation** | 15-30MB | <1MB |
| **Operation speed** | 50-200ms | <1ms |
| **Connection overhead** | New connection per operation | Single persistent connection |
| **Bootstrap complexity** | Manual configuration dance | Automatic graceful bootstrap |
| **Concurrent operations** | Limited by OS | Thousands |
| **Startup time** | Process spawn | Immediate |
| **File management** | Complex | None |
| **Failed record handling** | Manual cleanup | Automatic |
| **Network resilience** | Manual failover | Automatic with smart failback |

## 🏗️ Architecture

```
Traditional: PocketBase Collections → Config File Generation → NATS Reload
pb-nats: PocketBase Starts → Bootstrap Mode → NATS Configured → Auto-Connect → Real-time Updates
```

### Enhanced Connection Management

- **Graceful Bootstrap** → Starts without NATS, connects when available
- **Persistent Connection** → Single long-lived NATS connection shared across all operations
- **Automatic Failover** → Smart failover to backup servers with exponential backoff
- **Intelligent Failback** → Automatic return to primary server when healthy
- **Connection Health Monitoring** → Built-in reconnection and health checks

### Core Components
- **System Bootstrap** → Automatic operator and system account generation
- **Accounts** → NATS Accounts (with built-in isolation boundaries)
- **Users** → NATS Users (with role-based permissions within accounts)  
- **Roles** → Permission templates
- **Connection Manager** → Persistent NATS connection with failover and bootstrap
- **Failed Record Management** → Automatic cleanup with configurable retention

## 📦 Installation

```bash
go get github.com/skeeeon/pb-nats
```

## 🚀 Quick Start

### Basic Setup (Bootstrap Mode)

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
    
    // Setup NATS JWT integration - starts in bootstrap mode
    if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup NATS sync: %v", err)
    }
    
    // Start PocketBase - works even without NATS running!
    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

**What happens:**
1. ✅ PocketBase starts successfully (even without NATS)
2. 🔄 System operator JWT is generated automatically
3. 📊 Operations are queued until NATS becomes available
4. 🎯 Automatic connection when you start NATS server

### Production Setup with Failover

```go
options := pbnats.DefaultOptions()
options.NATSServerURL = "nats://primary.company.com:4222"
options.BackupNATSServerURLs = []string{
    "nats://backup1.company.com:4222",
    "nats://backup2.company.com:4222",
}

// Graceful bootstrap + high availability
if err := pbnats.Setup(app, options); err != nil {
    log.Fatalf("Failed to setup NATS sync: %v", err)
}
```

## 📋 PocketBase Collection Schema

The library automatically creates and manages the following collections:

### 1. System Operator Collection (`nats_system_operator`) - Hidden

Contains the operator JWT needed for NATS server configuration.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Operator name |
| `jwt` | Text | **Operator JWT for NATS server configuration** |
| `public_key` | Text | Operator public key (auto-generated) |
| `seed` | Text | Operator seed (auto-generated) |
| `signing_*` | Text | Signing keys (auto-generated) |

### 2. Accounts Collection (`nats_accounts`)

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

### 3. NATS Users Collection (`nats_users`)

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

### 4. Roles Collection (`nats_roles`)

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

### 5. Publish Queue Collection (`nats_publish_queue`) - Hidden

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

### Basic Configuration

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

// Apply the configuration
if err := pbnats.Setup(app, options); err != nil {
    log.Fatalf("Failed to setup NATS JWT sync: %v", err)
}
```

### Advanced Connection Management

```go
options := pbnats.DefaultOptions()

// NATS server configuration with failover
options.NATSServerURL = "nats://primary.company.com:4222"
options.BackupNATSServerURLs = []string{
    "nats://backup1.company.com:4222",
    "nats://backup2.company.com:4222",
}

// Custom retry configuration
options.ConnectionRetryConfig = &pbtypes.RetryConfig{
    MaxPrimaryRetries: 6,                   // Retries before failover
    InitialBackoff:    500 * time.Millisecond, // Initial retry delay
    MaxBackoff:        10 * time.Second,    // Maximum retry delay
    BackoffMultiplier: 1.5,                 // Backoff progression
    FailbackInterval:  15 * time.Second,    // Primary health check frequency
}

// Custom timeout configuration
options.ConnectionTimeouts = &pbtypes.TimeoutConfig{
    ConnectTimeout: 10 * time.Second,   // Connection establishment timeout
    PublishTimeout: 30 * time.Second,   // Publish operation timeout  
    RequestTimeout: 30 * time.Second,   // Request operation timeout
}
```

### Failed Record Management Configuration

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

### Default Permissions Configuration

```go
// Default permissions for users when role permissions are empty
// Note: Accounts provide isolation boundaries, no scoping needed
options.DefaultPublishPermissions = []string{">"}               // Full access within account
options.DefaultSubscribePermissions = []string{">", "_INBOX.>"} // Full access + inbox within account

// Custom event filtering
options.EventFilter = func(collectionName, eventType string) bool {
    // Only process certain events
    return true
}
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

# Get operator JWT for NATS server configuration (admin only)
GET /api/collections/nats_system_operator/records
Authorization: Bearer {admin_token}

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

### Bootstrap Flow
1. **PocketBase Starts**: Initializes successfully even without NATS
2. **System Components**: Operator, system account, role, and user are created
3. **Bootstrap Mode**: Connection manager starts in bootstrap mode
4. **NATS Configuration**: Extract operator JWT and configure NATS server
5. **NATS Startup**: Start NATS server with operator configuration
6. **Auto-Connect**: pb-nats detects NATS and establishes connection
7. **Queue Processing**: All pending operations are processed

### Main Processing Flow
1. **Collection Changes**: User creates/updates account, user, or role
2. **JWT Generation**: Library generates appropriate JWTs using pure Go libraries
3. **Queue Publishing**: Changes are queued for reliable processing with debouncing
4. **Persistent Connection**: JWTs are published via persistent NATS connection with automatic failover
5. **Real-time Updates**: Users immediately get new permissions without server restarts

### Enhanced Connection Management
1. **Graceful Bootstrap**: Starts without NATS, connects when available
2. **Persistent Connection**: Single long-lived connection shared across operations
3. **Smart Failover**: Automatic failover to backup servers with exponential backoff
4. **Intelligent Failback**: Background monitoring returns to primary when healthy
5. **Connection Health**: Built-in reconnection logic and connection state monitoring

### Failed Record Management
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
Persistent NATS Connection (with failover) → 
Immediate Permission Changes

Bootstrap Mode:
PocketBase Start (No NATS) → 
System Component Creation → 
Bootstrap Connection Manager → 
NATS Server Configuration → 
NATS Server Start → 
Auto-Connect → 
Queue Processing

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
4. **Bootstrap Connection**: Connection manager starts in bootstrap mode
5. **Publisher**: Background queue processor starts with bootstrap awareness
6. **Hooks**: PocketBase event hooks are registered
7. **Ready**: System is ready to process changes (with or without NATS)

## 🏭 Production Setup

### NATS Server Configuration

```conf
# /etc/nats/nats.conf
# Use the operator JWT from pb-nats system operator collection
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

### Production Bootstrap Workflow

```bash
# 1. Start PocketBase with pb-nats
./pocketbase serve

# 2. Extract operator JWT
curl -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  "http://localhost:8090/api/collections/nats_system_operator/records" \
  | jq -r '.items[0].jwt' > /etc/nats/operator.jwt

# 3. Configure NATS server with operator JWT
# Edit /etc/nats/nats.conf with operator path

# 4. Start NATS server  
nats-server -c /etc/nats/nats.conf

# 5. Check pb-nats logs for successful connection
# Look for: "Bootstrap successful! Connected to NATS server"
```

### Production Monitoring

The library provides comprehensive logging for production monitoring:

```go
options := pbnats.DefaultOptions()
options.LogToConsole = true  // Enable detailed logging

// Monitor these log patterns:
// - "Bootstrap mode: NATS connection will be established when server becomes available"
// - "Bootstrap successful! Connected to NATS server: <url>"
// - "Connected to NATS server: <url>"
// - "Connection failed, retrying primary: attempt X/Y"
// - "Failed over to backup server: <url>"
// - "Failed back to primary server: <url>"
// - "Bootstrap mode: X operations queued, waiting for NATS connection"
// - "Queue processing complete: X processed, Y failed"
// - "Cleaning up N old failed queue records"
```

### High Availability Configuration

```go
options := pbnats.DefaultOptions()

// Production NATS cluster with backup servers
options.NATSServerURL = "nats://nats1.company.com:4222"
options.BackupNATSServerURLs = []string{
    "nats://nats2.company.com:4222",
    "nats://nats3.company.com:4222",
    "nats://nats-dr.company.com:4222",
}

// Aggressive failover for high availability
options.ConnectionRetryConfig = &pbtypes.RetryConfig{
    MaxPrimaryRetries: 3,              // Quick failover
    InitialBackoff:    100 * time.Millisecond, // Fast initial retry
    MaxBackoff:        2 * time.Second, // Short max backoff
    BackoffMultiplier: 2.0,            // Standard progression
    FailbackInterval:  10 * time.Second, // Frequent primary checks
}

// Short timeouts for quick failure detection
options.ConnectionTimeouts = &pbtypes.TimeoutConfig{
    ConnectTimeout: 3 * time.Second,
    PublishTimeout: 5 * time.Second,
    RequestTimeout: 5 * time.Second,
}
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
    
    // Setup NATS JWT authentication with graceful bootstrap
    if err := pbnats.Setup(app, pbnats.DefaultOptions()); err != nil {
        log.Fatalf("Failed to setup NATS: %v", err)
    }
    
    app.Start()
}
```

## 🎯 Use Cases

### IoT Data Platform with Bootstrap
```go
// Start PocketBase first (without NATS)
// Configure NATS with extracted operator JWT
// NATS automatically connects and processes queued operations
// Account: "smart-building-corp"
// Users: sensor managers, data analysts, operators
// Permissions: 
//   - Sensors can publish: "sensors.*.telemetry"  
//   - Analysts can subscribe: "sensors.>"
//   - Operators can publish alerts: "alerts.>"
// Resilient connections handle sensor network interruptions
```

### Multi-Tenant SaaS
```go
// Each tenant gets their own account
// Users isolated to their account's data streams
// Graceful bootstrap allows staged deployment
// Admins can access cross-account monitoring streams via imports/exports
// Real-time permission updates as subscriptions change
// Automatic cleanup of failed synchronization attempts
// High availability with automatic failover to backup NATS servers
```

### Development Teams
```go
// Accounts: "frontend-team", "backend-team", "devops-team"  
// Each team gets isolated communication channels
// Bootstrap mode allows development without complex NATS setup
// Cross-team collaboration channels with explicit permissions
// CI/CD systems get service account access
// Persistent connections reduce deployment overhead
```

## 🐛 Troubleshooting

### Bootstrap Issues

**Q: PocketBase starts but no operator JWT is generated**
A: Check that the system initialization completed successfully. Look for log messages about system component creation. The operator JWT should appear in the `nats_system_operator` collection.

**Q: NATS server won't start with the operator JWT**
A: Verify the JWT format and ensure there are no extra characters. The JWT should be a single line starting with `eyJ`. Check NATS server logs for specific JWT validation errors.

**Q: pb-nats stays in bootstrap mode even after NATS starts**
A: Check network connectivity between pb-nats and NATS server. Verify the `NATSServerURL` in your configuration matches your NATS server address. Look for connection attempt logs.

**Q: Panic with "invalid memory address or nil pointer dereference" during shutdown**
A: This was a race condition in the connection manager that has been resolved. The error typically occurred when PocketBase was shutting down (like when specifying HTTP serve addresses). The fix ensures proper cleanup of background goroutines and ticker resources.

### Connection Issues

**Q: JWTs not updating in NATS**
A: If in bootstrap mode, check that NATS connection has been established. Look for "Bootstrap successful!" in logs. If connected, verify that your system user has proper permissions and NATS server is reachable.

**Q: Connection keeps failing over to backup servers**
A: Check primary NATS server health and network connectivity. The system will automatically fail back once the primary is healthy. Review your `ConnectionRetryConfig` settings if failover is too sensitive.

**Q: Users can't connect to NATS** 
A: Verify the user's account is active and role has appropriate permissions. Check that permissions don't include invalid subject patterns.

**Q: Permission denied errors**
A: Check that permissions are valid NATS subject patterns and don't include scoping placeholders (accounts provide isolation).

**Q: JWT regeneration not working**
A: Ensure the `regenerate` field is being set to `true` and the user has permission to update their own record.

**Q: Failed records building up in database**
A: Check your `FailedRecordCleanupInterval` and `FailedRecordRetentionTime` settings. Ensure the cleanup process is running (check logs for "Cleaning up N old failed queue records").

**Q: Frequent connection failures**
A: Review your `ConnectionTimeouts` configuration. Network issues may require longer timeouts. Check NATS server logs for connection limits or authentication issues.

### Debug Logging

```go
options := pbnats.DefaultOptions()
options.LogToConsole = true  // Enable detailed logging

// Check logs for:
// - Bootstrap mode initialization
// - System component creation  
// - JWT generation
// - Connection establishment and health
// - Failover and failback events
// - Queue processing
// - NATS publishing results
// - Failed record cleanup operations
```

### Connection Status Monitoring

The connection manager provides status information for debugging:

```go
// Access connection status (for debugging/monitoring tools)
// Note: This is internal - not exposed in public API
status := connectionManager.GetConnectionStatus()
// status.Connected - whether currently connected
// status.CurrentURL - which server we're connected to  
// status.FailoverState - healthy/retrying/failed_over/bootstrapping
// status.LastFailover - when last failover occurred
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
5. **Bootstrap awareness**: Records showing "bootstrap mode" messages are waiting for NATS connection

## 📚 Examples

Check the `examples/` directory for:
- `basic/` - Simple setup with graceful bootstrap
- `advanced/` - Custom configuration with pb-audit integration and advanced connection management
- `integration/` - Complete workflow demonstration with JWT regeneration and bootstrap process

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
