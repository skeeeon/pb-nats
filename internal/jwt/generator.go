// Package jwt provides JWT generation for NATS authentication
package jwt

import (
	"fmt"
	"strings"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
	"github.com/pocketbase/pocketbase"
	"github.com/skeeeon/pb-nats/internal/nkey"
)

// Generator handles generating NATS JWTs
type Generator struct {
	app         *pocketbase.PocketBase
	nkeyManager *nkey.Manager
	options     pbnats.Options
}

// NewGenerator creates a new JWT generator
func NewGenerator(app *pocketbase.PocketBase, nkeyManager *nkey.Manager, options pbnats.Options) *Generator {
	return &Generator{
		app:         app,
		nkeyManager: nkeyManager,
		options:     options,
	}
}

// GenerateOperatorJWT generates a JWT for the operator
func (g *Generator) GenerateOperatorJWT(operator *pbnats.SystemOperatorRecord) (string, error) {
	// Create operator key pair from seed
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operator.Seed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator key pair: %w", err)
	}

	// Create operator claims
	operatorClaims := jwt.NewOperatorClaims(operator.PublicKey)
	operatorClaims.Name = operator.Name
	
	// Add signing key
	operatorClaims.SigningKeys.Add(operator.SigningPublicKey)

	// Encode the JWT
	jwtValue, err := operatorClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode operator JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateAccountJWT generates a JWT for an organization (NATS account)
func (g *Generator) GenerateAccountJWT(org *pbnats.OrganizationRecord, operatorSigningSeed string) (string, error) {
	// Create operator signing key pair
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operatorSigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator signing key pair: %w", err)
	}

	// Create account claims
	accountClaims := jwt.NewAccountClaims(org.PublicKey)
	accountClaims.Name = org.NormalizeAccountName()
	
	// Add signing key
	accountClaims.SigningKeys.Add(org.SigningPublicKey)

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

// GenerateUserJWT generates a JWT for a user
func (g *Generator) GenerateUserJWT(user *pbnats.NatsUserRecord, org *pbnats.OrganizationRecord, role *pbnats.RoleRecord) (string, error) {
	// Create account signing key pair
	accountKP, err := g.nkeyManager.KeyPairFromSeed(org.SigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create account signing key pair: %w", err)
	}

	// Create user claims
	userClaims := jwt.NewUserClaims(user.PublicKey)
	userClaims.Name = user.NatsUsername
	userClaims.IssuerAccount = org.PublicKey
	userClaims.BearerToken = user.BearerToken

	// Set expiration if specified
	if user.JWTExpiresAt != nil {
		userClaims.Expires = user.JWTExpiresAt.Unix()
	} else if g.options.DefaultJWTExpiry > 0 {
		userClaims.Expires = jwt.TimeFromDuration(g.options.DefaultJWTExpiry)
	}

	// Apply permissions from role
	if err := g.applyRolePermissions(userClaims, user, org, role); err != nil {
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
func (g *Generator) GenerateCredsFile(user *pbnats.NatsUserRecord) (string, error) {
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

// applyRolePermissions applies role-based permissions to user claims
func (g *Generator) applyRolePermissions(userClaims *jwt.UserClaims, user *pbnats.NatsUserRecord, org *pbnats.OrganizationRecord, role *pbnats.RoleRecord) error {
	orgName := org.NormalizeAccountName()
	username := user.NatsUsername

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
		publishPerms = []string{g.options.DefaultUserPublish}
	}
	if len(subscribePerms) == 0 {
		subscribePerms = g.options.DefaultUserSubscribe
	}

	// Apply organization and user scoping
	scopedPublishPerms := pbnats.ApplyUserScope(publishPerms, orgName, username)
	scopedSubscribePerms := pbnats.ApplyUserScope(subscribePerms, orgName, username)

	// Set permissions in claims
	for _, perm := range scopedPublishPerms {
		userClaims.Permissions.Pub.Allow.Add(perm)
	}
	for _, perm := range scopedSubscribePerms {
		userClaims.Permissions.Sub.Allow.Add(perm)
	}

	return nil
}

// applyRoleLimits applies role-based limits to user claims
func (g *Generator) applyRoleLimits(userClaims *jwt.UserClaims, role *pbnats.RoleRecord) {
	// Apply connection limits
	if role.MaxConnections > 0 {
		userClaims.Limits.NatsLimits.Conn = role.MaxConnections
	} else if role.MaxConnections == -1 {
		userClaims.Limits.NatsLimits.Conn = -1 // Unlimited
	}

	// Apply data limits
	if role.MaxData > 0 {
		userClaims.Limits.NatsLimits.Data = role.MaxData
	} else if role.MaxData == -1 {
		userClaims.Limits.NatsLimits.Data = -1 // Unlimited
	}

	// Apply payload limits
	if role.MaxPayload > 0 {
		userClaims.Limits.NatsLimits.Payload = role.MaxPayload
	} else if role.MaxPayload == -1 {
		userClaims.Limits.NatsLimits.Payload = -1 // Unlimited
	}
}

// GenerateSystemAccountJWT generates a JWT for the system account
func (g *Generator) GenerateSystemAccountJWT(sysAccount *pbnats.OrganizationRecord, operatorSigningSeed string) (string, error) {
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

	// No limits for system account
	accountClaims.Limits.JetStreamLimits.DiskStorage = -1
	accountClaims.Limits.JetStreamLimits.MemoryStorage = -1

	// Encode the JWT
	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode system account JWT: %w", err)
	}

	return jwtValue, nil
}
