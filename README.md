# PocketBase NATS JWT Authentication

A Go library for extending [PocketBase](https://pocketbase.io/) for managing [NATS Server](https://nats.io/) using JWT-based authentication. Automatically generates and manages NATS operator, account, and user nKeys and JWTs in real-time through PocketBase hooks, eliminating traditional configuration file management.

## Key Features

- **Real-time JWT Sync**: PocketBase CRUD hooks trigger JWT generation and publish to NATS via `$SYS.REQ.CLAIMS.UPDATE`
- **Account-Based Multi-Tenancy**: NATS accounts provide hard isolation boundaries without subject scoping
- **Cross-Account Communication**: Account-level imports and exports for sharing streams and services between accounts
- **Graceful Bootstrap**: Starts without NATS running, operator JWT generated in-memory for initial NATS config
- **Persistent Connections**: Single NATS connection with automatic failover to backup servers and exponential backoff
- **Multiple Signing Keys**: Graceful and emergency key rotation per account
- **Two-Tier Permissions**: Role baseline + optional per-user overrides (union merge), with allow/deny semantics
- **Two-Tier Limits**: Account-level (shared) and role-level (per-user) resource limits
- **Queue-Based Publishing**: Reliable JWT publishing with retry, deduplication, and automatic cleanup
- **Optional At-Rest Encryption**: AES-256-GCM encryption of sensitive fields using PocketBase's built-in security helpers
- **Response Permissions**: Request-reply pattern support with configurable limits
- **Locked-Down Defaults**: All collection API rules default to `nil` — consuming apps explicitly grant access

## Installation

```bash
go get github.com/skeeeon/pb-nats
```

## Quick Start

```go
package main

import (
    "log"
    "github.com/pocketbase/pocketbase"
    pbnats "github.com/skeeeon/pb-nats"
)

func main() {
    app := pocketbase.New()

    options := pbnats.DefaultOptions()
    options.NATSServerURL = "nats://localhost:4222"
    options.OperatorName = "my-operator"

    if err := pbnats.Setup(app, options); err != nil {
        log.Fatalf("Failed to setup NATS sync: %v", err)
    }

    pbnats.RegisterCommands(app)

    if err := app.Start(); err != nil {
        log.Fatal(err)
    }
}
```

## Bootstrap Process

**Problem**: Need operator JWT to configure NATS, but pb-nats needs NATS running.

**Solution**: Graceful bootstrap — JWT generated in-memory first, then exported via CLI.

### Step 1: Initialize PocketBase
```bash
./myapp serve
# Create a superuser when prompted, then stop the server (Ctrl+C)
```

### Step 2: Export NATS Configuration
```bash
./myapp nats export --output ./nats-config/

# Creates:
#   operator.jwt      - Operator JWT for NATS resolver
#   operator.conf     - Operator config with system account
#   nats.conf         - Ready-to-use NATS server config
#   README.txt        - Setup instructions
```

### Step 3: Start NATS Server
```bash
cd nats-config
mkdir -p ./jwt ./storage/jetstream
nats-server -c nats.conf
```

### Step 4: Start PocketBase
```bash
./myapp serve
# pb-nats connects and begins syncing JWTs
```

### CLI Export Options
```bash
./myapp nats export --output ./nats-config/     # All files to directory
./myapp nats export --operator-jwt              # Operator JWT to stdout
./myapp nats export --config                    # nats.conf to stdout
./myapp nats export --operator-conf             # operator.conf to stdout

# Custom server settings
./myapp nats export --output ./nats-config/ \
  --server-name my-nats \
  --port 4222 \
  --jetstream-store /var/lib/nats/jetstream
```

## Architecture

### Data Flow

```
PocketBase CRUD (REST API)
    |
    v
Hooks (sync/) -> Generate keys/JWTs (nkey/, jwt/)
    |
    v
Queue for publish (publisher/)
    |
    v
NATS connection with failover (connection/)
    |
    v
NATS Server ($SYS.REQ.CLAIMS.UPDATE)
```

### Initialization Order

1. **Collections** — Creates 7 PocketBase collections (all API rules locked by default)
2. **NKey Manager** — Generates NATS NKey pairs (operator, account, user)
3. **JWT Generator** — Generates NATS JWTs with permissions, limits, imports, and exports
4. **System Components** — Creates operator, system account, system role, system user
5. **Publisher** — Starts persistent NATS connection with failover
6. **Sync Manager** — Registers PocketBase hooks for real-time sync

### Key Design Decisions

- **Account isolation** for multi-tenancy (not subject scoping)
- **Graceful bootstrap**: operator JWT generated before NATS is running
- **Two-tier permissions**: role provides baseline, per-user permissions merged via union
- **Two-tier limits**: account-level and role-based per-user limits
- **Cross-account imports/exports**: managed as separate collections, embedded in account JWTs
- **Multiple signing keys**: most recent key signs new JWTs, older keys remain valid
- **Locked collections by default**: consuming app sets API rules appropriate to its deployment model
- **NKeys stored in PocketBase** (PocketBase is the authority, optional encryption at rest)

## Collections

pb-nats creates 7 collections. All have `nil` API rules by default — the consuming app must explicitly configure access rules appropriate for its deployment.

### System Operator (`nats_system_operator`)

Internal collection — should remain locked. Contains the operator identity, signing keys, and JWT used for NATS server configuration.

| Field | Type | Hidden | Description |
|-------|------|--------|-------------|
| `name` | Text | | Operator name |
| `public_key` | Text | | Operator identity public key |
| `private_key` | Text | Yes | Operator private key |
| `seed` | Text | Yes | Operator seed |
| `signing_keys` | JSON | | Array of signing public keys |
| `signing_keys_private` | JSON | Yes | Array of signing key material |
| `jwt` | Text | | Operator JWT |
| `system_account_id` | Text | | Reference to system account record |

### Accounts (`nats_accounts`)

Each account is an isolation boundary in NATS. Users within an account cannot see traffic from other accounts.

| Field | Type | Hidden | Description |
|-------|------|--------|-------------|
| `name` | Text | | Account display name |
| `description` | Text | | Account description |
| `public_key` | Text | | Account public key |
| `private_key` | Text | Yes | Account private key |
| `seed` | Text | Yes | Account seed |
| `signing_keys` | JSON | | Array of signing public keys |
| `signing_keys_private` | JSON | Yes | Array of signing key material |
| `jwt` | Text | | Account JWT |
| `active` | Bool | | Account enabled/disabled |
| `add_signing_key` | Bool | | Trigger: append new signing key |
| `remove_signing_key` | Text | | Trigger: remove key by public key string |
| `rotate_keys` | Bool | | Trigger: emergency rotation (purge all, generate new) |
| `max_connections` | Number | | Max concurrent connections (-1=unlimited, 0=disabled) |
| `max_subscriptions` | Number | | Max subscriptions (-1=unlimited, 0=disabled) |
| `max_data` | Number | | Max bytes in-flight (-1=unlimited, 0=disabled) |
| `max_payload` | Number | | Max message size (-1=unlimited, 0=disabled) |
| `max_jetstream_disk_storage` | Number | | JetStream disk limit (-1=unlimited, 0=disabled) |
| `max_jetstream_memory_storage` | Number | | JetStream memory limit (-1=unlimited, 0=disabled) |

### Account Exports (`nats_account_exports`)

Declares subjects that an account makes available to other accounts. Supports both streams (continuous data flow) and services (request-reply).

| Field | Type | Description |
|-------|------|-------------|
| `account_id` | Relation | Owning account (cascade delete) |
| `name` | Text | Export name |
| `subject` | Text | NATS subject pattern (supports wildcards) |
| `type` | Select | `stream` or `service` |
| `token_req` | Bool | Require activation token for import |
| `response_type` | Select | `Singleton`, `Stream`, or `Chunked` (service only) |
| `response_threshold` | Number | Response timeout in milliseconds (service only) |
| `account_token_position` | Number | Position of account token in wildcard subject |
| `advertise` | Bool | Advertise this export |
| `allow_trace` | Bool | Allow trace (service only) |
| `description` | Text | Export description |

### Account Imports (`nats_account_imports`)

Consumes subjects exported by other accounts. The exporting account is referenced by public key, not by relation, since it may be in a different deployment.

| Field | Type | Description |
|-------|------|-------------|
| `account_id` | Relation | Importing account (cascade delete) |
| `name` | Text | Import name |
| `subject` | Text | Subject being imported |
| `account` | Text | Exporting account's public key |
| `token` | Text | Activation JWT (required when export has `token_req`) |
| `local_subject` | Text | Local subject remapping (supports `$1`, `$2` references) |
| `type` | Select | `stream` or `service` |
| `share` | Bool | Enable latency tracking (service only) |
| `allow_trace` | Bool | Allow trace (stream only) |
| `description` | Text | Import description |

### Roles (`nats_roles`)

Permission templates assigned to users. Defines allowed/denied subjects and per-user resource limits.

| Field | Type | Description |
|-------|------|-------------|
| `name` | Text | Role name |
| `description` | Text | Role description |
| `is_default` | Bool | Default role flag |
| `publish_permissions` | JSON | Allowed publish subjects |
| `subscribe_permissions` | JSON | Allowed subscribe subjects |
| `publish_deny_permissions` | JSON | Denied publish subjects (takes precedence) |
| `subscribe_deny_permissions` | JSON | Denied subscribe subjects (takes precedence) |
| `allow_response` | Bool | Enable request-reply response permissions |
| `allow_response_max` | Number | Max responses per request (-1=unlimited, 0=default/1) |
| `allow_response_ttl` | Number | Response TTL in seconds (0=no limit) |
| `max_subscriptions` | Number | Per-user subscription limit |
| `max_data` | Number | Per-user data limit |
| `max_payload` | Number | Per-user message size limit |

### Users (`nats_users`)

PocketBase auth collection with NATS integration. Each user belongs to one account and one role.

| Field | Type | Hidden | Description |
|-------|------|--------|-------------|
| `nats_username` | Text | | NATS username |
| `description` | Text | | User description |
| `account_id` | Relation | | Link to account |
| `role_id` | Relation | | Link to role |
| `public_key` | Text | | User public key |
| `private_key` | Text | Yes | User private key |
| `seed` | Text | Yes | User seed |
| `jwt` | Text | | User JWT |
| `creds_file` | Text | | Complete NATS .creds file for client connection |
| `bearer_token` | Bool | | Enable bearer token auth |
| `jwt_expires_at` | Date | | JWT expiration timestamp |
| `regenerate` | Bool | | Trigger: regenerate JWT |
| `active` | Bool | | User status |
| `publish_permissions` | JSON | | Per-user publish overrides (merged with role) |
| `subscribe_permissions` | JSON | | Per-user subscribe overrides (merged with role) |
| `publish_deny_permissions` | JSON | | Per-user publish deny overrides |
| `subscribe_deny_permissions` | JSON | | Per-user subscribe deny overrides |

### Publish Queue (`nats_publish_queue`)

Internal queue for reliable JWT publishing. Should remain locked.

| Field | Type | Description |
|-------|------|-------------|
| `account_id` | Relation | Account being published |
| `action` | Select | `upsert` or `delete` |
| `message` | Text | Error message on failure |
| `attempts` | Number | Retry count (0-10) |
| `failed_at` | Date | Set on permanent failure |

## Cross-Account Communication

NATS accounts are isolated by default. Imports and exports enable controlled cross-account communication without breaking isolation boundaries.

### Exports

An export declares a subject that other accounts can access. Two types:

- **Stream**: The exporting account publishes data, importing accounts subscribe. One-way data flow.
- **Service**: The importing account sends requests, the exporting account responds. Request-reply pattern.

```http
POST /api/collections/nats_account_exports/records
{
  "account_id": "ACCOUNT_RECORD_ID",
  "name": "sensor-data",
  "subject": "sensors.>",
  "type": "stream"
}
```

Service export with response configuration:
```http
POST /api/collections/nats_account_exports/records
{
  "account_id": "ACCOUNT_RECORD_ID",
  "name": "auth-service",
  "subject": "auth.validate",
  "type": "service",
  "response_type": "Singleton"
}
```

### Imports

An import consumes a subject exported by another account. The exporting account is referenced by its public key.

```http
POST /api/collections/nats_account_imports/records
{
  "account_id": "IMPORTING_ACCOUNT_RECORD_ID",
  "name": "sensor-data",
  "subject": "sensors.>",
  "account": "AABC...EXPORTING_ACCOUNT_PUBLIC_KEY",
  "type": "stream"
}
```

Import with local subject remapping:
```http
POST /api/collections/nats_account_imports/records
{
  "account_id": "IMPORTING_ACCOUNT_RECORD_ID",
  "name": "auth-service",
  "subject": "auth.validate",
  "account": "AABC...EXPORTING_ACCOUNT_PUBLIC_KEY",
  "type": "service",
  "local_subject": "external.auth.validate"
}
```

### Token-Required Exports

For restricted access, set `token_req: true` on the export. Importing accounts must provide an activation token (JWT) in the import's `token` field.

### How It Works

Exports and imports are embedded in the account JWT. When you create, update, or delete an export or import record, the owning account's JWT is automatically regenerated and published to NATS. No manual intervention required.

## Security

### Collection Access Rules

All collections default to `nil` (no API access). This is intentional — pb-nats is a library, and the consuming app is responsible for setting API rules appropriate for its deployment model.

**Recommended approach:**
- Keep `nats_system_operator` and `nats_publish_queue` locked (system-only)
- Set accounts, exports, and imports rules to allow trusted admin/service access
- Set roles rules to allow trusted admin/service access
- Set user rules to allow self-service credential retrieval

Example (consuming app's migration or setup code):
```go
accounts, _ := app.FindCollectionByNameOrId("nats_accounts")
accounts.ListRule = types.Pointer("@request.auth.id != '' && active = true")
accounts.ViewRule = types.Pointer("@request.auth.id != '' && active = true")
app.Save(accounts)
```

### Hidden Fields

Sensitive cryptographic material (`private_key`, `seed`, `signing_keys_private`) is marked `Hidden: true` on all collections. These fields are never included in API responses, regardless of access rules.

### At-Rest Encryption

Optional AES-256-GCM encryption of sensitive fields stored in the database, using PocketBase's built-in security helpers.

```go
options := pbnats.DefaultOptions()
options.EncryptionKey = "your-random-32-character-string!" // exactly 32 characters
```

**Encrypted fields**: `private_key`, `seed`, and `signing_keys_private` on operator, account, and user records.

**Not encrypted** (by design):
- `jwt` — public claims, needed for NATS resolver config
- `creds_file` — contains seed but left unencrypted for self-service download
- `public_key` — not sensitive

**Backward compatibility**: Encrypted values are stored with an `enc::` prefix. Values without the prefix are treated as plaintext. Existing unencrypted data works without migration — fields are encrypted on the next write.

**Key requirements**: Must be exactly 32 characters (AES-256). Validated at startup. Changing the key requires manual re-encryption of existing data.

### Multiple Signing Keys

Accounts and the operator support multiple signing keys. The most recently added key signs new JWTs, while older keys remain valid for existing JWTs.

**Add key (graceful rotation):**
```http
PATCH /api/collections/nats_accounts/records/{id}
{"add_signing_key": true}
```

**Remove key:**
```http
PATCH /api/collections/nats_accounts/records/{id}
{"remove_signing_key": "AABC...public_key_to_remove"}
```

**Emergency rotation (purge all, generate new):**
```http
PATCH /api/collections/nats_accounts/records/{id}
{"rotate_keys": true}
```

**Graceful rotation workflow:**
1. `{"add_signing_key": true}` — new key added, old JWTs still valid
2. Regenerate user JWTs at your own pace via `{"regenerate": true}` on each user
3. `{"remove_signing_key": "OLD_KEY"}` — revoke the old key

## Permission System

### Allow/Deny Semantics

Permissions follow NATS semantics: deny takes precedence over allow.

1. Check if subject matches any Allow pattern
2. If allowed, check if subject matches any Deny pattern
3. Deny wins on match

```json
{
  "name": "sensor_reader",
  "publish_permissions": ["sensors.>"],
  "subscribe_permissions": ["sensors.>", "alerts.>"],
  "publish_deny_permissions": ["sensors.internal.>"],
  "subscribe_deny_permissions": ["alerts.admin.>"]
}
```

### Per-User Permission Overrides

User-level permissions are **merged (union)** with role permissions — they extend the role's baseline, not replace it.

```json
{
  "publish_permissions": ["admin.reports.>"],
  "subscribe_permissions": ["admin.reports.>"]
}
```

If the role grants `sensors.>`, the user's resulting JWT contains both `sensors.>` and `admin.reports.>`.

Per-user deny permissions also merge with role deny permissions. When all permission fields are empty, the user inherits the role's permissions unchanged.

### Response Permissions

For request-reply patterns, configure on the role:

```json
{
  "allow_response": true,
  "allow_response_max": 1,
  "allow_response_ttl": 30
}
```

- `allow_response_max`: -1=unlimited, 0=default (1 response), positive=specific limit
- `allow_response_ttl`: 0=no expiration, positive=seconds

## Resource Limits

### Account-Level (Shared)

Set on the account record. Controls total resources across all users in the account:
- `max_connections`, `max_subscriptions`, `max_data`, `max_payload`
- `max_jetstream_disk_storage`, `max_jetstream_memory_storage`

### User-Level (Per-User)

Set on the role record. Controls individual user resource usage:
- `max_subscriptions`, `max_data`, `max_payload`

### Limit Values

| Value | Meaning |
|-------|---------|
| `-1` | Unlimited |
| `0` | **Disabled** (blocks access entirely) |
| positive | Specific limit (bytes, count, etc.) |

Setting a limit to `0` completely disables access for that resource.

## Configuration

```go
options := pbnats.DefaultOptions()

// NATS server
options.NATSServerURL = "nats://localhost:4222"
options.BackupNATSServerURLs = []string{"nats://backup1:4222", "nats://backup2:4222"}
options.OperatorName = "my-operator"

// Custom collection names (optional)
options.AccountCollectionName = "nats_accounts"
options.UserCollectionName = "nats_users"
options.RoleCollectionName = "nats_roles"
options.ExportCollectionName = "nats_account_exports"
options.ImportCollectionName = "nats_account_imports"

// Connection retry
options.ConnectionRetryConfig = &pbnats.RetryConfig{
    MaxPrimaryRetries: 4,           // attempts before trying backup
    InitialBackoff:    1 * time.Second,
    MaxBackoff:        8 * time.Second,
    BackoffMultiplier: 2.0,
    FailbackInterval:  30 * time.Second, // how often to try primary again
}

// Timeouts
options.ConnectionTimeouts = &pbnats.TimeoutConfig{
    ConnectTimeout: 5 * time.Second,
    PublishTimeout: 10 * time.Second,
    RequestTimeout: 10 * time.Second,
}

// Performance
options.PublishQueueInterval = 30 * time.Second  // queue processing interval
options.DebounceInterval = 3 * time.Second       // batch rapid changes

// Cleanup
options.FailedRecordCleanupInterval = 6 * time.Hour
options.FailedRecordRetentionTime = 24 * time.Hour

// Default permissions (when role permissions are empty)
options.DefaultPublishPermissions = []string{">"}
options.DefaultSubscribePermissions = []string{">", "_INBOX.>"}

// JWT
options.DefaultJWTExpiry = 0 // never expires

// Security
options.EncryptionKey = "" // empty = disabled, 32 chars = enabled

// Event filtering
options.EventFilter = func(collection, event string) bool {
    return true // process all events
}
```

### CLI Command Options

```go
pbnats.RegisterCommandsWithOptions(app, pbnats.CommandOptions{
    DefaultServerName:     "my-nats-server",
    DefaultPort:           4222,
    DefaultJetstreamStore: "/var/lib/nats/jetstream",
    DefaultOutputDir:      "./nats-config",
})
```

## Client Connection

```javascript
const pb = new PocketBase('http://localhost:8090');
await pb.collection('nats_users').authWithPassword('user@example.com', 'password');

const user = await pb.collection('nats_users').getOne(pb.authStore.record.id);

import { connect, credsAuthenticator } from 'nats';
const nc = await connect({
    servers: ["nats://your-server:4222"],
    authenticator: credsAuthenticator(new TextEncoder().encode(user.creds_file))
});
```

## Error Handling

pb-nats exports typed errors with classification helpers:

```go
if pbnats.IsTemporaryError(err) {
    // Network/timeout - retry
}
if pbnats.IsPermanentError(err) {
    // Invalid config/auth - don't retry
}
if pbnats.IsConfigurationError(err) {
    // Setup issue - fail fast
}
```

## Troubleshooting

**Bootstrap**: If NATS isn't running, pb-nats operates in bootstrap mode. Export config with `nats export`, start NATS, then restart PocketBase.

**Permission issues**: Check deny permissions (both role and user level). Deny takes precedence. Per-user permissions are additive (union), not replacements.

**Signing key issues**: After `rotate_keys`, all user JWTs are invalid — regenerate explicitly. After `remove_signing_key`, only JWTs signed by that key are invalidated.

**Resource limits**: `-1` = unlimited, `0` = **disabled** (blocks access), positive = specific limit.

**Encryption**: If you enable encryption on an existing deployment, data encrypts on next write. Changing the key without re-encryption breaks reads of previously encrypted data.

**Cross-account imports/exports**: Changes to export or import records automatically regenerate the owning account's JWT and publish it to NATS. The exporting account's public key (not record ID) is used in imports, so external accounts from other deployments are supported.

## License

MIT License - see LICENSE file for details.
