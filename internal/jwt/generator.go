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

// Generator handles generating NATS JWTs
type Generator struct {
	app         *pocketbase.PocketBase
	nkeyManager *nkey.Manager
	options     pbtypes.Options
}

// NewGenerator creates a new JWT generator
func NewGenerator(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options pbtypes.Options) *Generator {
	return &Generator{
		app:         app,
		nkeyManager: nkeyManager,
		options:     options,
	}
}

// GenerateOperatorJWT generates a JWT for the operator
// systemAccountPubKey should be provided to properly designate the system account
func (g *Generator) GenerateOperatorJWT(operator *pbtypes.SystemOperatorRecord, systemAccountPubKey string) (string, error) {
	// Create operator key pair from seed
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operator.Seed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator key pair: %w", err)
	}

	// Create operator claims
	operatorClaims := jwt.NewOperatorClaims(operator.PublicKey)
	operatorClaims.Name = operator.Name
	
	// **CRITICAL FIX**: Specify which account is the system account
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

// GenerateOperatorJWTWithoutSystemAccount generates operator JWT without system account reference
// Used for initial operator creation before system account exists
func (g *Generator) GenerateOperatorJWTWithoutSystemAccount(operator *pbtypes.SystemOperatorRecord) (string, error) {
	return g.GenerateOperatorJWT(operator, "")
}

// GenerateAccountJWT generates a JWT for an account
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

	// Set default limits (unlimited)
	accountClaims.Limits.JetStreamLimits.DiskStorage = -1
	accountClaims.Limits.JetStreamLimits.MemoryStorage = -1
	accountClaims.Limits.AccountLimits.Conn = -1
	accountClaims.Limits.NatsLimits.Subs = -1
	accountClaims.Limits.NatsLimits.Data = -1
	accountClaims.Limits.NatsLimits.Payload = -1

	// Encode the JWT
	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode account JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateUserJWT generates a JWT for a user within an account
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

// GenerateCredsFile generates a complete .creds file for a user
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

// isSystemUser checks if a user is a system user that needs unscoped permissions
func (g *Generator) isSystemUser(user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord) bool {
	return account.NormalizeName() == "SYS" && user.NatsUsername == "sys"
}

// applyRolePermissions applies role-based permissions to user claims
// No scoping applied - accounts provide isolation boundary
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

// applyRoleLimits applies role-based limits to user claims
// Note: Based on NATS JWT v2 library structure and nats-tower patterns
func (g *Generator) applyRoleLimits(userClaims *jwt.UserClaims, role *pbtypes.RoleRecord) {
	// Apply data limits (correct field name from NATS JWT library)
	if role.MaxData > 0 {
		userClaims.Limits.Data = role.MaxData
	} else if role.MaxData == -1 {
		userClaims.Limits.Data = jwt.NoLimit // Unlimited
	}

	// Apply subscription limits 
	if role.MaxConnections > 0 {
		userClaims.Limits.Subs = role.MaxConnections
	} else if role.MaxConnections == -1 {
		userClaims.Limits.Subs = jwt.NoLimit // Unlimited
	}

	// Apply payload limits
	if role.MaxPayload > 0 {
		userClaims.Limits.Payload = role.MaxPayload
	} else if role.MaxPayload == -1 {
		userClaims.Limits.Payload = jwt.NoLimit // Unlimited
	}
}

// GenerateSystemAccountJWT generates a JWT for the system account
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

	// **CRITICAL FIX**: Explicitly disable JetStream for system account
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
