// Package jwt provides JWT generation for NATS authentication
package jwt

import (
	"fmt"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/pocketbase/pocketbase"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Generator handles generating NATS JWTs for operators, accounts, and users.
// This is the core component responsible for creating the JWT hierarchy that
// establishes trust relationships in NATS.
//
// JWT HIERARCHY:
// Operator JWT (root of trust) → Account JWT (signed by operator) → User JWT (signed by account)
type Generator struct {
	app         *pocketbase.PocketBase
	nkeyManager *nkey.Manager
	options     pbtypes.Options
}

// NewGenerator creates a new JWT generator with PocketBase integration.
//
// PARAMETERS:
//   - app: PocketBase application instance for database access
//   - nkeyManager: NKey manager for key operations
//   - options: Configuration options including default permissions
//
// RETURNS:
// - Generator instance ready for JWT creation
func NewGenerator(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options pbtypes.Options) *Generator {
	return &Generator{
		app:         app,
		nkeyManager: nkeyManager,
		options:     options,
	}
}

// GenerateOperatorJWT generates a NATS operator JWT that serves as the root of trust.
// The operator JWT is used in nats.conf and validates all account JWTs in the system.
//
// OPERATOR JWT CONTENTS:
// - Operator identity (public key)
// - Authorized signing keys for creating account JWTs  
// - System account designation (for monitoring and management)
//
// PARAMETERS:
//   - operator: System operator record containing keys and identity
//   - systemAccountPubKey: Public key of system account. If empty, operator JWT
//     is created without system account designation (bootstrap mode only)
//
// BEHAVIOR:
// - Creates operator claims with identity and signing keys
// - Designates system account if public key provided
// - Signs JWT with operator's main key pair
//
// RETURNS:
// - Encoded JWT string for nats.conf configuration
// - error if key operations or JWT encoding fails
//
// USAGE IN NATS CONFIG:
//   operator: /path/to/operator.jwt
//
// SIDE EFFECTS: None (pure JWT generation)
func (g *Generator) GenerateOperatorJWT(operator *pbtypes.SystemOperatorRecord, systemAccountPubKey string) (string, error) {
	// Create operator key pair from seed
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operator.Seed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator key pair: %w", err)
	}

	// Create operator claims
	operatorClaims := jwt.NewOperatorClaims(operator.PublicKey)
	operatorClaims.Name = operator.Name
	
	// **CRITICAL**: Specify which account is the system account
	// This is required for NATS to properly recognize the system account
	if systemAccountPubKey != "" {
		operatorClaims.SystemAccount = systemAccountPubKey
	}
	
	// Add signing key
	operatorClaims.SigningKeys.Add(operator.SigningPublicKey)

	// Encode the JWT
	jwtValue, err := operatorClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode operator JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateOperatorJWTWithoutSystemAccount generates operator JWT for bootstrap mode.
// Used during initial setup before system account exists.
//
// BOOTSTRAP USAGE:
// This is called during system initialization when no system account exists yet.
// The operator JWT is later updated to include the system account reference.
//
// PARAMETERS:
//   - operator: System operator record
//
// RETURNS:
// - Operator JWT without system account designation
// - error if JWT generation fails
func (g *Generator) GenerateOperatorJWTWithoutSystemAccount(operator *pbtypes.SystemOperatorRecord) (string, error) {
	return g.GenerateOperatorJWT(operator, "")
}

// GenerateAccountJWT generates a NATS account JWT for tenant isolation.
// Account JWTs define boundaries and permissions for groups of users.
//
// ACCOUNT JWT CONTENTS:
// - Account identity (public key)
// - Signing keys authorized to create user JWTs
// - Account-level limits and permissions
// - Export/import declarations (for cross-account communication)
//
// PARAMETERS:
//   - account: Account record containing keys and metadata
//   - operatorSigningSeed: Operator signing key for JWT signature
//
// BEHAVIOR:
// - Creates account claims with identity and signing keys
// - Sets account name (normalized for NATS compatibility)
// - Applies configurable limits from account record (or unlimited if not set)
// - Signs JWT with operator signing key
//
// RETURNS:
// - Encoded account JWT for publishing to NATS via $SYS.REQ.CLAIMS.UPDATE
// - error if key operations or JWT encoding fails
//
// NATS PUBLISHING:
// The returned JWT is published to NATS server via:
//   $SYS.REQ.CLAIMS.UPDATE with account JWT as payload
//
// SIDE EFFECTS: None (pure JWT generation)
func (g *Generator) GenerateAccountJWT(account *pbtypes.AccountRecord, operatorSigningSeed string) (string, error) {
	// Create operator signing key pair
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operatorSigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator signing key pair: %w", err)
	}

	// Create account claims
	accountClaims := jwt.NewAccountClaims(account.PublicKey)
	accountClaims.Name = account.NormalizeName()
	
	// Add signing key
	accountClaims.SigningKeys.Add(account.SigningPublicKey)

	// Apply configurable limits from account record (default to unlimited if not set)
	g.applyAccountLimits(accountClaims, account)

	// Encode the JWT
	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode account JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateUserJWT generates a NATS user JWT with role-based permissions.
// User JWTs are signed by account signing keys and contain specific permissions.
//
// USER JWT CONTENTS:
// - User identity (public key)
// - Account association (issuer account)
// - Role-based permissions (publish/subscribe subjects)
// - Connection limits and constraints
// - Optional expiration time
//
// PARAMETERS:
//   - user: User record containing identity and settings
//   - account: Account record for JWT signing and association
//   - role: Role record containing permissions and limits
//
// BEHAVIOR:
// - Creates user claims with identity and account association
// - Applies role-based permissions (no subject scoping needed - accounts provide isolation)
// - Sets connection and data limits from role
// - Applies JWT expiration if configured
// - Signs JWT with account signing key
//
// RETURNS:
// - Encoded user JWT for inclusion in .creds file
// - error if permission processing or JWT encoding fails
//
// PERMISSION STRATEGY:
// Accounts provide isolation boundaries, so permissions are applied directly:
// - "sensors.temperature" within account A ≠ "sensors.temperature" within account B
// - No complex scoping like "account_a.sensors.temperature" needed
//
// SIDE EFFECTS: None (pure JWT generation)
func (g *Generator) GenerateUserJWT(user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord, role *pbtypes.RoleRecord) (string, error) {
	// Create account signing key pair
	accountKP, err := g.nkeyManager.KeyPairFromSeed(account.SigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create account signing key pair: %w", err)
	}

	// Create user claims
	userClaims := jwt.NewUserClaims(user.PublicKey)
	userClaims.Name = user.NatsUsername
	userClaims.IssuerAccount = account.PublicKey
	userClaims.BearerToken = user.BearerToken

	// Set expiration if specified
	if user.JWTExpiresAt != nil {
		userClaims.Expires = user.JWTExpiresAt.Unix()
	} else if g.options.DefaultJWTExpiry > 0 {
		userClaims.Expires = time.Now().Add(g.options.DefaultJWTExpiry).Unix()
	}

	// Apply permissions from role (no scoping - account provides isolation)
	if err := g.applyRolePermissions(userClaims, user, account, role); err != nil {
		return "", fmt.Errorf("failed to apply role permissions: %w", err)
	}

	// Apply limits from role
	g.applyRoleLimits(userClaims, role)

	// Encode the JWT
	jwtValue, err := userClaims.Encode(accountKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode user JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateCredsFile generates a complete .creds file for NATS client connection.
// The .creds file contains both the user JWT and the user's private key.
//
// CREDS FILE FORMAT:
// -----BEGIN NATS USER JWT-----
// <user JWT>
// ------END NATS USER JWT------
// 
// ************************* IMPORTANT *************************
// NKEY Seed printed below can be used to sign and prove identity.
// NKEYs are sensitive and should be treated as secrets.
// 
// -----BEGIN USER NKEY SEED-----
// <user seed>
// ------END USER NKEY SEED------
//
// PARAMETERS:
//   - user: User record containing JWT and seed
//
// BEHAVIOR:
// - Validates user JWT and seed are present
// - Formats credentials using NATS standard format
// - Returns complete .creds file ready for client use
//
// RETURNS:
// - Formatted .creds file as string
// - error if JWT or seed missing, or formatting fails
//
// CLIENT USAGE:
//   nc, err := nats.Connect("nats://server:4222", 
//     nats.UserCredentials("path/to/user.creds"))
//
// SIDE EFFECTS: None (pure formatting)
func (g *Generator) GenerateCredsFile(user *pbtypes.NatsUserRecord) (string, error) {
	if user.JWT == "" {
		return "", fmt.Errorf("user JWT is empty, cannot generate creds file")
	}

	if user.Seed == "" {
		return "", fmt.Errorf("user seed is empty, cannot generate creds file")
	}

	// Use NATS JWT utility to format the creds file
	creds, err := jwt.FormatUserConfig(user.JWT, []byte(user.Seed))
	if err != nil {
		return "", fmt.Errorf("failed to format user config: %w", err)
	}

	return string(creds), nil
}

// applyAccountLimits applies configurable account-level limits to account JWT claims.
// Account limits control resources across the entire account.
//
// LIMIT TYPES:
// - Connection limits: Total concurrent connections to the account
// - Data limits: Total bytes in-flight across all users in account
// - Subscription limits: Total subscriptions across all users in account
// - JetStream limits: Storage and stream limits for the account
//
// PARAMETERS:
//   - accountClaims: JWT account claims to modify
//   - account: Account record containing limit configuration
//
// BEHAVIOR:
// - Uses configured limits from account record if set (> 0)
// - Uses unlimited (-1) if account limits are 0 or not set
// - Applies jwt.NoLimit constant for unlimited values
//
// RETURNS: None (modifies accountClaims directly)
//
// SIDE EFFECTS:
// - Modifies accountClaims.Limits fields
func (g *Generator) applyAccountLimits(accountClaims *jwt.AccountClaims, account *pbtypes.AccountRecord) {
	// Apply JetStream limits
	if account.MaxJetStreamDiskStorage > 0 {
		accountClaims.Limits.JetStreamLimits.DiskStorage = account.MaxJetStreamDiskStorage
	} else if account.MaxJetStreamDiskStorage == -1 {
		accountClaims.Limits.JetStreamLimits.DiskStorage = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.JetStreamLimits.DiskStorage = jwt.NoLimit
	}
	
	if account.MaxJetStreamMemoryStorage > 0 {
		accountClaims.Limits.JetStreamLimits.MemoryStorage = account.MaxJetStreamMemoryStorage
	} else if account.MaxJetStreamMemoryStorage == -1 {
		accountClaims.Limits.JetStreamLimits.MemoryStorage = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.JetStreamLimits.MemoryStorage = jwt.NoLimit
	}

	// Apply account-level connection limits
	if account.MaxConnections > 0 {
		accountClaims.Limits.AccountLimits.Conn = account.MaxConnections
	} else if account.MaxConnections == -1 {
		accountClaims.Limits.AccountLimits.Conn = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.AccountLimits.Conn = jwt.NoLimit
	}

	// Apply NATS limits
	if account.MaxSubscriptions > 0 {
		accountClaims.Limits.NatsLimits.Subs = account.MaxSubscriptions
	} else if account.MaxSubscriptions == -1 {
		accountClaims.Limits.NatsLimits.Subs = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.NatsLimits.Subs = jwt.NoLimit
	}
	
	if account.MaxData > 0 {
		accountClaims.Limits.NatsLimits.Data = account.MaxData
	} else if account.MaxData == -1 {
		accountClaims.Limits.NatsLimits.Data = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.NatsLimits.Data = jwt.NoLimit
	}
	
	if account.MaxPayload > 0 {
		accountClaims.Limits.NatsLimits.Payload = account.MaxPayload
	} else if account.MaxPayload == -1 {
		accountClaims.Limits.NatsLimits.Payload = jwt.NoLimit
	} else {
		// Default to unlimited if not set (0 or missing)
		accountClaims.Limits.NatsLimits.Payload = jwt.NoLimit
	}
}

// isSystemUser checks if a user belongs to the system account and requires special permissions.
// System users need unrestricted access for NATS management operations.
//
// PARAMETERS:
//   - user: User record to check
//   - account: Account record to check
//
// BEHAVIOR:
// - Checks if account is system account ("SYS")
// - Checks if user is system user ("sys")
//
// RETURNS:
// - true if user is system user requiring special permissions
// - false for regular users
func (g *Generator) isSystemUser(user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord) bool {
	return account.NormalizeName() == "SYS" && user.NatsUsername == "sys"
}

// applyRolePermissions applies role-based permissions to user JWT claims.
// Permissions are applied directly without scoping since accounts provide isolation.
//
// PERMISSION PHILOSOPHY:
// - Account boundaries provide isolation (no subject scoping needed)
// - Role permissions applied directly within account context
// - System users get special unrestricted permissions
// - Default permissions applied when role permissions empty
//
// PARAMETERS:
//   - userClaims: JWT user claims to modify
//   - user: User record for context
//   - account: Account record for context
//   - role: Role record containing permissions
//
// BEHAVIOR:
// - Extracts publish/subscribe permissions from role
// - Applies system permissions for system users
// - Applies default permissions for empty role permissions
// - Sets permissions directly in JWT claims (no scoping)
//
// RETURNS:
// - nil on success
// - error if permission parsing fails
//
// SIDE EFFECTS:
// - Modifies userClaims.Permissions.Pub.Allow and Sub.Allow
func (g *Generator) applyRolePermissions(userClaims *jwt.UserClaims, user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord, role *pbtypes.RoleRecord) error {
	// Get publish permissions from role
	publishPerms, err := role.GetPublishPermissions()
	if err != nil {
		return fmt.Errorf("failed to get publish permissions: %w", err)
	}

	// Get subscribe permissions from role
	subscribePerms, err := role.GetSubscribePermissions()
	if err != nil {
		return fmt.Errorf("failed to get subscribe permissions: %w", err)
	}

	// Apply default permissions if role permissions are empty
	if len(publishPerms) == 0 {
		if g.isSystemUser(user, account) {
			// System users get unrestricted access
			publishPerms = []string{"$SYS.>", ">"}
		} else {
			publishPerms = g.options.DefaultPublishPermissions
		}
	}
	if len(subscribePerms) == 0 {
		if g.isSystemUser(user, account) {
			// System users get unrestricted access
			subscribePerms = []string{"$SYS.>", ">"}
		} else {
			subscribePerms = g.options.DefaultSubscribePermissions
		}
	}

	// Apply permissions directly - no scoping needed since accounts provide isolation
	var finalPublishPerms, finalSubscribePerms []string

	if g.isSystemUser(user, account) {
		// System users get raw permissions without any modification
		finalPublishPerms = publishPerms
		finalSubscribePerms = subscribePerms
	} else {
		// Regular users get permissions as-is within their account boundary
		finalPublishPerms = publishPerms
		finalSubscribePerms = subscribePerms
	}

	// Set permissions in claims
	for _, perm := range finalPublishPerms {
		userClaims.Permissions.Pub.Allow.Add(perm)
	}
	for _, perm := range finalSubscribePerms {
		userClaims.Permissions.Sub.Allow.Add(perm)
	}

	return nil
}

// applyRoleLimits applies role-based per-user limits to user JWT claims.
// These limits are enforced by NATS server to prevent resource exhaustion per user.
//
// LIMIT TYPES:
// - Data limits: Total bytes user can have in flight
// - Subscription limits: Maximum concurrent subscriptions per user
// - Payload limits: Maximum size of individual messages
//
// PARAMETERS:
//   - userClaims: JWT user claims to modify
//   - role: Role record containing limit definitions
//
// BEHAVIOR:
// - Applies data limits from role (0 = disabled, -1 = unlimited, >0 = limit in bytes)
// - Applies subscription limits from role (correctly mapped from max_subscriptions field)
// - Applies payload limits from role
// - Uses jwt.NoLimit constant for unlimited values
//
// RETURNS: None (modifies userClaims directly)
//
// SIDE EFFECTS:
// - Modifies userClaims.Limits fields
func (g *Generator) applyRoleLimits(userClaims *jwt.UserClaims, role *pbtypes.RoleRecord) {
	// Apply data limits (correct field name from NATS JWT library)
	if role.MaxData > 0 {
		userClaims.Limits.Data = role.MaxData
	} else if role.MaxData == -1 {
		userClaims.Limits.Data = jwt.NoLimit // Unlimited
	}

	// Apply subscription limits (FIXED: correctly mapped from MaxSubscriptions)
	if role.MaxSubscriptions > 0 {
		userClaims.Limits.Subs = role.MaxSubscriptions
	} else if role.MaxSubscriptions == -1 {
		userClaims.Limits.Subs = jwt.NoLimit // Unlimited
	}

	// Apply payload limits
	if role.MaxPayload > 0 {
		userClaims.Limits.Payload = role.MaxPayload
	} else if role.MaxPayload == -1 {
		userClaims.Limits.Payload = jwt.NoLimit // Unlimited
	}
}

// GenerateSystemAccountJWT generates a specialized JWT for the system account (SYS).
// The system account has special exports for monitoring and management operations.
//
// SYSTEM ACCOUNT FEATURES:
// - Exports monitoring services ($SYS.REQ.ACCOUNT.*.*)
// - Exports account monitoring streams ($SYS.ACCOUNT.*.>)
// - JetStream explicitly disabled for system account
// - Unlimited NATS core limits for administrative operations
//
// PARAMETERS:
//   - sysAccount: System account record
//   - operatorSigningSeed: Operator signing key for JWT signature
//
// BEHAVIOR:
// - Creates system account claims with special exports
// - Disables JetStream for system account (required by NATS)
// - Sets unlimited core NATS limits
// - Signs with operator signing key
//
// RETURNS:
// - System account JWT for NATS configuration
// - error if JWT generation fails
//
// NATS SYSTEM INTEGRATION:
// This JWT enables NATS server to expose monitoring APIs and handle
// account management requests through the system account.
//
// SIDE EFFECTS: None (pure JWT generation)
func (g *Generator) GenerateSystemAccountJWT(sysAccount *pbtypes.AccountRecord, operatorSigningSeed string) (string, error) {
	// Create operator signing key pair
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operatorSigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator signing key pair: %w", err)
	}

	// Create account claims for system account
	accountClaims := jwt.NewAccountClaims(sysAccount.PublicKey)
	accountClaims.Name = "SYS"
	
	// Add signing key
	accountClaims.SigningKeys.Add(sysAccount.SigningPublicKey)

	// System account has special exports for monitoring
	accountClaims.Exports = jwt.Exports{
		&jwt.Export{
			Name:                 "account-monitoring-services",
			Subject:              "$SYS.REQ.ACCOUNT.*.*",
			Type:                 jwt.Service,
			ResponseType:         jwt.ResponseTypeStream,
			AccountTokenPosition: 4,
			Info: jwt.Info{
				Description: "Request account specific monitoring services for: SUBSZ, CONNZ, LEAFZ, JSZ and INFO",
				InfoURL:     "https://docs.nats.io/nats-server/configuration/sys_accounts",
			},
		},
		&jwt.Export{
			Name:                 "account-monitoring-streams",
			Subject:              "$SYS.ACCOUNT.*.>",
			Type:                 jwt.Stream,
			AccountTokenPosition: 3,
			Info: jwt.Info{
				Description: "Account specific monitoring stream",
				InfoURL:     "https://docs.nats.io/nats-server/configuration/sys_accounts",
			},
		},
	}

	// **CRITICAL**: Explicitly disable JetStream for system account
	// System accounts should never have JetStream enabled
	accountClaims.Limits.JetStreamLimits.DiskStorage = 0  // Disabled
	accountClaims.Limits.JetStreamLimits.MemoryStorage = 0  // Disabled
	accountClaims.Limits.JetStreamLimits.Streams = 0  // No streams allowed
	accountClaims.Limits.JetStreamLimits.Consumer = 0  // No consumers allowed
	
	// Set reasonable limits for system operations
	accountClaims.Limits.AccountLimits.Conn = jwt.NoLimit  // Unlimited connections
	accountClaims.Limits.NatsLimits.Subs = jwt.NoLimit     // Unlimited subscriptions
	accountClaims.Limits.NatsLimits.Data = jwt.NoLimit     // Unlimited data
	accountClaims.Limits.NatsLimits.Payload = jwt.NoLimit  // Unlimited payload

	// Encode the JWT
	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode system account JWT: %w", err)
	}

	return jwtValue, nil
}
