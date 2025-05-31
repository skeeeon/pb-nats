# PocketBase NATS JWT Authentication

A high-performance library for seamless integration between [PocketBase](https://pocketbase.io/) and [NATS Server](https://nats.io/) using modern JWT-based authentication. This library automatically generates and manages NATS JWTs in real-time, eliminating the need for traditional configuration files.

## 🚀 Key Features

- **⚡ High-Performance JWT Generation**: Sub-millisecond operations using pure Go libraries
- **🔄 Real-time Synchronization**: Direct JWT publishing to NATS servers via `$SYS.REQ.CLAIMS.UPDATE`
- **🏢 Organization-Based Architecture**: Map PocketBase organizations to NATS accounts
- **🔐 Native PocketBase Security**: Leverage built-in authentication and record-level permissions
- **📊 Role-Based Permissions**: Flexible permission system with organization scoping
- **🔧 Zero Configuration Files**: No file management, all data stored in PocketBase database
- **⚡ Queue-Based Publishing**: Reliable JWT publishing with retry logic and debouncing
- **🛡️ Production Ready**: Built on battle-tested patterns with comprehensive error handling

## 📈 Performance Benefits

| Metric | Traditional Config | JWT Library |
|--------|-------------------|-------------|
| **Memory per operation** | 15-30MB | <1MB |
| **Operation speed** | 50-200ms | <1ms |
| **Concurrent operations** | Limited by OS | Thousands |
| **Startup time** | Process spawn | Immediate |
| **File management** | Complex | None |

## 🏗️ Architecture

```
OLD: PocketBase Collections → Config File Generation → NATS Reload
NEW: PocketBase Collections → Direct JWT Generation → NATS Account Publishing
```

### Core Components
- **Organizations** → NATS Accounts (with isolated permissions)
- **Users** → NATS Users (with role-based permissions)  
- **Roles** → Permission templates
- **System Components** → Auto-managed operator and system account

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
        log.Fatalf("Failed to setup NATS JWT sync: %v", err)
    }
    
    // Start the PocketBase app as usual
    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

## 📋 PocketBase Collection Schema

The library automatically creates and manages the following collections:

### 1. Organizations Collection (`organizations`)

Maps to NATS accounts and provides organization-level isolation.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Organization display name |
| `account_name` | Text | NATS account name (auto-normalized) |
| `description` | Text | Organization description |
| `public_key` | Text | Account public key (auto-generated) |
| `private_key` | Text | Account private key (auto-generated) |
| `seed` | Text | Account seed (auto-generated) |
| `signing_public_key` | Text | Account signing public key (auto-generated) |
| `signing_private_key` | Text | Account signing private key (auto-generated) |
| `signing_seed` | Text | Account signing seed (auto-generated) |
| `jwt` | Text | Account JWT (auto-generated) |
| `active` | Boolean | Whether the organization is active |
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
| `organization_id` | Relation | Link to organizations |
| `role_id` | Relation | Link to nats_roles |
| `public_key` | Text | User public key (auto-generated) |
| `private_key` | Text | User private key (auto-generated) |
| `seed` | Text | User seed (auto-generated) |
| `jwt` | Text | User JWT (auto-generated) |
| `creds_file` | Text | Complete .creds file (auto-generated) |
| `bearer_token` | Boolean | Enable bearer token authentication |
| `jwt_expires_at` | DateTime | Optional JWT expiration |
| `active` | Boolean | Whether the user is active |

### 3. Roles Collection (`nats_roles`)

Defines permission templates for users.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `publish_permissions` | Text | JSON array of publish topic patterns |
| `subscribe_permissions` | Text | JSON array of subscribe topic patterns |
| `is_default` | Boolean | Whether this is a default role |
| `max_connections` | Number | Max connections (-1 = unlimited) |
| `max_data` | Number | Max data limit (-1 = unlimited) |
| `max_payload` | Number | Max payload size (-1 = unlimited) |

### Permission Examples

For the JSON permission fields, store values as properly formatted JSON arrays:

```json
// Publish permissions
["acme.sensors.*.telemetry", "acme.alerts.>"]

// Subscribe permissions  
["acme.>", "_INBOX.>"]
```

## ⚙️ Configuration Options

```go
options := pbnats.DefaultOptions()

// Collection names (if you want custom names)
options.UserCollectionName = "my_nats_users"
options.RoleCollectionName = "my_nats_roles"
options.OrganizationCollectionName = "my_organizations"

// NATS configuration
options.NATSServerURL = "nats://your-server:4222"
options.OperatorName = "your-operator-name"

// Performance tuning
options.PublishQueueInterval = 30 * time.Second  // Queue processing frequency
options.DebounceInterval = 3 * time.Second       // Debounce rapid changes

// JWT settings
options.DefaultJWTExpiry = 24 * time.Hour        // Set expiration (0 = never expires)

// Default permissions (organization-scoped)
options.DefaultOrgPublish = "{org}.>"            // {org} gets replaced
options.DefaultOrgSubscribe = []string{"{org}.>", "_INBOX.>"}

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

// Organization admins can access users in their org
collection.ViewRule = `
  (@request.auth.id = id) ||
  (@request.auth.role = 'org_admin' && @request.auth.organization_id = organization_id) ||
  @request.auth.role = 'admin'
`
```

### Field-Level Visibility

```go
// Hide sensitive fields from unauthorized users
collection.ViewRule = `
  @request.auth.id = id ? "*" : 
  "id,nats_username,active,organization_id"
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

# List users in specific organization
GET /api/collections/nats_users/records?filter=organization_id="{org_id}"
Authorization: Bearer {admin_token}
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

// Now you can publish/subscribe with your permissions
await nc.publish("your.org.sensors.temp", JSON.stringify({temp: 23.5}));
```

## 🔧 Organization Scoping

The library automatically applies organization scoping to all permissions:

```go
// Role permission: "sensors.*.telemetry"
// Gets scoped to: "acme_corp.sensors.*.telemetry" for organization "acme-corp"

// User permission: "{org}.user.{user}.>"  
// Gets scoped to: "acme_corp.user.john_doe.>" for user "john.doe" in "acme-corp"
```

## 📊 How It Works

1. **Collection Changes**: User creates/updates organization, user, or role
2. **JWT Generation**: Library generates appropriate JWTs using pure Go libraries
3. **Queue Publishing**: Changes are queued for reliable processing with debouncing
4. **NATS Publishing**: JWTs are published directly to NATS via `$SYS.REQ.CLAIMS.UPDATE`
5. **Real-time Updates**: Users immediately get new permissions without server restarts

### Event Flow
```
PocketBase Record Change → 
Debounced Processing → 
JWT Generation → 
Queue Publishing → 
NATS Server Update → 
Immediate Permission Changes
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
// Organization: "smart-building-corp"
// Users: sensor managers, data analysts, operators
// Permissions: 
//   - Sensors can publish: "smart_building_corp.sensors.*.telemetry"  
//   - Analysts can subscribe: "smart_building_corp.sensors.>"
//   - Operators can publish alerts: "smart_building_corp.alerts.>"
```

### Multi-Tenant SaaS
```go
// Each tenant gets their own organization
// Users isolated to their tenant's data streams
// Admins can access cross-tenant monitoring streams
// Real-time permission updates as subscriptions change
```

### Development Teams
```go
// Organizations: "frontend-team", "backend-team", "devops-team"  
// Each team gets isolated communication channels
// Cross-team collaboration channels with explicit permissions
// CI/CD systems get service account access
```

## 🐛 Troubleshooting

### Common Issues

**Q: JWTs not updating in NATS**
A: Check that your system user has proper permissions and NATS server is reachable. Verify the system account JWT is properly configured.

**Q: Users can't connect to NATS** 
A: Verify the user's organization is active and role has appropriate permissions. Check that the organization scoping is working correctly.

**Q: Permission denied errors**
A: Check that organization scoping is working correctly and permissions include the full scoped subject.

**Q: Race conditions during startup**
A: The library now has proper initialization ordering. If you still see issues, enable console logging to debug the startup sequence.

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

## 📚 Examples

Check the `examples/` directory for:
- `basic/` - Simple setup with default options
- `advanced/` - Custom configuration with pb-audit integration  
- `integration/` - Complete workflow demonstration

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
