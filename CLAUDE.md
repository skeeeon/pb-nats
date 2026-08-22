# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

pb-nats is a Go library that integrates PocketBase with NATS Server using JWT-based authentication. It automatically generates and manages NATS operator/account/user JWTs, syncing them to NATS in real-time via PocketBase hooks. It is not a standalone application — it's imported as a library into PocketBase apps.

## Build & Test Commands

```bash
go build ./...              # Build all packages
go test ./...               # Run all tests
go test -v ./pkg/name       # Run tests for a specific package
go test -run TestName ./... # Run a single test by name
go vet ./...                # Static analysis
go fmt ./...                # Format code
```

Examples can be built/run individually: `go run ./examples/basic`, `go run ./examples/advanced`, `go run ./examples/integration`.

## Architecture

### Public API (root package `pbnats`)

- **`Setup(app, options)`** — Entry point. Registers a PocketBase bootstrap hook that initializes all components in dependency order. Returns immediately; actual init happens at bootstrap.
- **`RegisterCommands(app)`** — Adds `nats export` CLI commands (operator JWT, nats.conf, operator.conf).
- **`DefaultOptions()`** — Returns default configuration. Callers override fields as needed.
- **`errors.go`** — Typed errors with classification (transient vs permanent, retryable, etc.).

### Internal Packages (`internal/`)

Initialization flows top-down through these in order:

1. **collections/** — Creates/manages 5 PocketBase collections: `nats_system_operator`, `nats_accounts`, `nats_roles`, `nats_users`, `nats_publish_queue`
2. **nkey/** — Generates NATS NKey pairs (operator, account, user)
3. **jwt/** — Generates NATS JWTs with limits and permissions applied
4. **connection/** — Persistent single NATS connection with failover to backup servers, exponential backoff, and bootstrap mode (works without NATS running)
5. **publisher/** — Queue-based reliable JWT publishing to NATS via `$SYS.REQ.CLAIMS.UPDATE`. Survives NATS outages with retry.
6. **sync/** — PocketBase `onCreate/onUpdate/onDelete` hooks that trigger JWT regeneration and publishing. Includes debouncing.
7. **types/** — Shared data structures (AccountRecord, NatsUserRecord, RoleRecord, Options, etc.) and consolidated `RecordTo*Model` converters (`converters.go`)
8. **utils/** — Logger and helpers

### Data Flow

```
PocketBase CRUD (REST API) → Hooks (sync/) → Generate keys/JWTs (nkey/, jwt/)
  → Queue for publish (publisher/) → NATS connection (connection/)
  → NATS Server ($SYS.REQ.CLAIMS.UPDATE)
```

### Key Design Decisions

- **Account-based isolation** for multi-tenancy (not subject scoping)
- **Graceful bootstrap**: operator JWT generated in-memory first, admin exports config via CLI, then NATS starts with that config — solves the chicken-and-egg problem
- **Two-tier permissions**: role-based permissions provide a baseline, optional per-user permissions are merged via union
- **Two-tier limits**: account-level limits (connections, data, JetStream) and role-based per-user limits
- **Multiple signing keys**: accounts/operator support multiple signing keys stored as JSON arrays (`signing_keys` public, `signing_keys_private` hidden). Most recent key signs new JWTs. Managed via triggers: `add_signing_key`, `remove_signing_key`, `rotate_keys` (emergency)
- **NKeys stored as plaintext** in PocketBase (by design — PocketBase is the authority)
- **`active` is durable state, triggers are verbs**: `active` (accounts and users) declares where a record should be and is **edge-triggered** against `record.Original()`, so unrelated saves never churn credentials. Triggers (`revoke`, `regenerate`, `rotate_keys`, `add_signing_key`, `remove_signing_key`) are one-shot actions that clear themselves. Account `active: false` withdraws via `$SYS.REQ.CLAIMS.DELETE` (reversible); user `active: false` revokes without reissuing; user `revoke` rotates the whole key pair. Every bulk reissue path (`regenerateUsersWithRole`, `regenerateUsersInAccount`) must skip inactive users, or an unrelated role edit hands a suspended user a JWT stamped after the revocation cutoff — valid again — plus a fresh `creds_file` to download
- **Startup reconciliation**: `sync.Manager.ReconcileAccounts()` enqueues every active account at boot, since account JWTs live only in the NATS resolver directory and hook-driven sync can't recover a lost one
- **Signing-key removal reissues users**: NATS rejects a user JWT whose issuer is absent from the account's signing keys, so the account hook detects a shrunken key set (`signingKeysRemoved`) and reissues that account's active users. `rotate_keys` additionally clears the revocation list, since those entries can never apply again
- **The system user never expires**: `DefaultJWTExpiry` and `jwt_expires_at` are skipped for it — it authenticates pb-nats' own NATS connection and nothing renews it. Client JWT renewal is deliberately the client's responsibility
- **The system account is generated, not edited**: `GenerateAccountJWT` delegates it to `GenerateSystemAccountJWT`, because its name (`SYS`), its two monitoring exports and its zeroed JetStream limits are NATS conventions rather than record fields — export/import records attached to it are ignored for the same reason. The system path used to run *only* at creation, so every later regeneration went through the ordinary one and silently renamed the account to `system_account` (via `NormalizeName`) while dropping both exports. Regeneration is routine: the account update hook has no system-account exemption, so a superuser editing the record hits it, as does revoking, suspending or deleting any user in the system account (the revocation is embedded by regenerating the owning account). The result is quiet because the *after*-update hook does skip the system account, so nothing publishes it — the mangled JWT sits in the database until `nats export` bakes it into `operator.conf` as the resolver preload, and only then becomes the SYS account of a freshly started server
- **NATS management replies are checked**: `$SYS.REQ.CLAIMS.UPDATE`/`DELETE` report rejections in the reply envelope's `error` field rather than by failing the request, so `checkClaimsResponse` inspects it. The server hardcodes error code 500 for every failure, so `description` is the only usable signal
- **A field is only wired if the converter reads it.** Declaring a collection field, watching it in `userJWTFieldsChanged`, documenting it, and testing the generator against it can all pass while the feature is dead. `jwt_expires_at` is the worked example: `RecordToUserModel` never populated `JWTExpiresAt`, so the generator's `user.JWTExpiresAt != nil` branch could not fire — a per-user expiry silently did nothing and `DefaultJWTExpiry` always won. `TestGenerateUserJWTExpiry` kept passing because it sets the model field directly, one layer *below* the gap. Pin new fields at the record→model boundary (`internal/types/converters_test.go`) against a real PocketBase record, not a hand-built model
- **Every field added to an existing collection needs an `ensure*Fields` pass.** Each `create*Collection` returns early when its collection already exists, so a field added only there reaches fresh installs and nowhere else. Both halves of the omission are silent: nothing adds the field, and PocketBase discards writes to fields a collection does not declare *without erroring*, so the code that sets it reports success forever. `system_account_id` is the worked example — added to `createSystemOperatorCollection` long after that collection shipped, so on every upgraded deployment `initializeSystemComponents` resolved the system account by name and saved the id on each boot while persisting nothing; `publisher.getSystemUser` then failed, `StartBootstrap` was never called, and the process sat in bootstrap mode for its whole life making *zero* connection attempts. A NATS server whose resolver directory is already populated hides this completely — it surfaces only against an empty resolver, where `ReconcileAccounts` is simultaneously the repair and the thing that cannot run. When adding a field, add the migration in the same commit, and test it by building the *old* collection shape and asserting a write round-trips afterwards (`TestEnsureSystemOperatorFields_AddsSystemAccountID`); asserting only that the field exists passes against a collection that still drops the value

## Key Dependencies

- `github.com/pocketbase/pocketbase` — Core framework being extended
- `github.com/nats-io/jwt/v2`, `nats.go`, `nkeys` — NATS JWT generation, client, and key cryptography
- `github.com/spf13/cobra` — CLI commands
