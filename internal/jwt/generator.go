// Package jwt provides JWT generation for NATS authentication
package jwt

import (
	"fmt"
	"time"

	jwt "github.com/nats-io/jwt/v2"
	"github.com/skeeeon/pb-nats/internal/nkey"
	pbtypes "github.com/skeeeon/pb-nats/internal/types"
)

// Generator handles generating NATS JWTs for operators, accounts, and users.
type Generator struct {
	nkeyManager     *nkey.Manager
	options         pbtypes.Options
	systemAccountID string
}

// NewGenerator creates a new JWT generator.
func NewGenerator(nkeyManager *nkey.Manager, options pbtypes.Options) *Generator {
	return &Generator{
		nkeyManager: nkeyManager,
		options:     options,
	}
}

// SetSystemAccountID records the system account's record ID, used to identify
// the system user for special permission handling. Called once the system
// account is known during initialization.
func (g *Generator) SetSystemAccountID(id string) {
	g.systemAccountID = id
}

// GenerateOperatorJWT generates a NATS operator JWT that serves as the root of trust.
func (g *Generator) GenerateOperatorJWT(operator *pbtypes.SystemOperatorRecord, systemAccountPubKey string) (string, error) {
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operator.Seed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator key pair: %w", err)
	}

	operatorClaims := jwt.NewOperatorClaims(operator.PublicKey)
	operatorClaims.Name = operator.Name

	if systemAccountPubKey != "" {
		operatorClaims.SystemAccount = systemAccountPubKey
	}

	for _, pubKey := range operator.AllSigningPublicKeys() {
		operatorClaims.SigningKeys.Add(pubKey)
	}

	jwtValue, err := operatorClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode operator JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateAccountJWT generates a NATS account JWT for tenant isolation.
// Exports and imports enable cross-account communication and are embedded in the JWT.
func (g *Generator) GenerateAccountJWT(account *pbtypes.AccountRecord, operatorSigningSeed string, exports []*pbtypes.AccountExportRecord, imports []*pbtypes.AccountImportRecord) (string, error) {
	// The system account is not an ordinary account. Its name (`SYS`), its two
	// monitoring exports and its zeroed JetStream limits are fixed by NATS
	// convention rather than by the record, and GenerateSystemAccountJWT is the
	// only place that knows them — but it used to run only at creation. Every
	// later regeneration came through here instead, renaming the account to
	// `system_account` via NormalizeName and dropping both exports, which is what
	// account-scoped monitoring ($SYS.REQ.ACCOUNT.*) imports.
	//
	// Regeneration is not hypothetical. The account update hook has no
	// system-account exemption, so a superuser editing the record hits it, and
	// revoking, suspending or deleting any user in the system account regenerates
	// its owning account to embed the revocation. The damage is quiet too: the
	// after-update hook skips the system account, so nothing publishes the mangled
	// JWT — it sits in the database until `nats export` bakes it into
	// operator.conf as the resolver preload, where it becomes the SYS account for
	// a freshly started server.
	if g.isSystemAccount(account) {
		return g.GenerateSystemAccountJWT(account, operatorSigningSeed)
	}

	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operatorSigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator signing key pair: %w", err)
	}

	accountClaims := jwt.NewAccountClaims(account.PublicKey)
	accountClaims.Name = account.NormalizeName()
	for _, pubKey := range account.AllSigningPublicKeys() {
		accountClaims.SigningKeys.Add(pubKey)
	}

	g.applyAccountLimits(accountClaims, account)
	g.applyAccountExports(accountClaims, exports)
	g.applyAccountImports(accountClaims, imports)
	if err := g.applyAccountRevocations(accountClaims, account); err != nil {
		return "", fmt.Errorf("failed to apply account revocations: %w", err)
	}

	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode account JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateUserJWT generates a NATS user JWT with role-based permissions.
// Now supports allow permissions, deny permissions, and response permissions.
func (g *Generator) GenerateUserJWT(user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord, role *pbtypes.RoleRecord) (string, error) {
	latestKey := account.LatestSigningKey()
	if latestKey == nil {
		return "", fmt.Errorf("account has no signing keys")
	}
	accountKP, err := g.nkeyManager.KeyPairFromSeed(latestKey.Seed)
	if err != nil {
		return "", fmt.Errorf("failed to create account signing key pair: %w", err)
	}

	userClaims := jwt.NewUserClaims(user.PublicKey)
	userClaims.Name = user.NatsUsername
	userClaims.IssuerAccount = account.PublicKey
	userClaims.BearerToken = user.BearerToken

	// Set expiration if specified.
	//
	// The system user is exempt: its JWT authenticates pb-nats' own persistent
	// connection to NATS, and nothing renews it. An expiry here — whether from a
	// per-user date or a deployment-wide DefaultJWTExpiry — would silently kill JWT
	// synchronization the moment it elapsed. Renewal is the client's job, and this
	// credential has no client.
	if !g.isSystemUser(user, account) {
		if user.JWTExpiresAt != nil {
			userClaims.Expires = user.JWTExpiresAt.Unix()
		} else if g.options.DefaultJWTExpiry > 0 {
			userClaims.Expires = time.Now().Add(g.options.DefaultJWTExpiry).Unix()
		}
	}

	// Apply permissions from role and user-level overrides (merged via union)
	if err := g.applyPermissions(userClaims, user, account, role); err != nil {
		return "", fmt.Errorf("failed to apply permissions: %w", err)
	}

	// Apply limits from role
	g.applyRoleLimits(userClaims, role)

	jwtValue, err := userClaims.Encode(accountKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode user JWT: %w", err)
	}

	return jwtValue, nil
}

// GenerateCredsFile generates a complete .creds file for NATS client connection.
func (g *Generator) GenerateCredsFile(user *pbtypes.NatsUserRecord) (string, error) {
	if user.JWT == "" {
		return "", fmt.Errorf("user JWT is empty, cannot generate creds file")
	}
	if user.Seed == "" {
		return "", fmt.Errorf("user seed is empty, cannot generate creds file")
	}

	creds, err := jwt.FormatUserConfig(user.JWT, []byte(user.Seed))
	if err != nil {
		return "", fmt.Errorf("failed to format user config: %w", err)
	}

	return string(creds), nil
}

// limitValue maps stored limit values to NATS JWT limit semantics:
// negative = unlimited (jwt.NoLimit), 0 = disabled, positive = specific limit.
func limitValue(v int64) int64 {
	if v < 0 {
		return jwt.NoLimit
	}
	return v
}

// applyAccountLimits applies configurable account-level limits to account JWT claims.
func (g *Generator) applyAccountLimits(accountClaims *jwt.AccountClaims, account *pbtypes.AccountRecord) {
	if g.isSystemAccount(account) {
		// A NATS server refuses to start when the system account has JetStream
		// enabled: "jetstream can not be enabled on the system account". The
		// system account record is created with -1 (unlimited) JetStream
		// storage, so regenerating its JWT through this path would produce a
		// token no server will accept — and because that JWT is what
		// `nats export` preloads into operator.conf, the failure lands at
		// server startup rather than anywhere near the change that caused it.
		//
		// GenerateAccountJWT now hands the system account to
		// GenerateSystemAccountJWT, which zeroes these itself, so this branch is a
		// backstop rather than the only thing standing between a NATS server and a
		// refusal to start.
		accountClaims.Limits.JetStreamLimits.DiskStorage = 0
		accountClaims.Limits.JetStreamLimits.MemoryStorage = 0
		accountClaims.Limits.JetStreamLimits.Streams = 0
		accountClaims.Limits.JetStreamLimits.Consumer = 0
	} else {
		accountClaims.Limits.JetStreamLimits.DiskStorage = limitValue(account.MaxJetStreamDiskStorage)
		accountClaims.Limits.JetStreamLimits.MemoryStorage = limitValue(account.MaxJetStreamMemoryStorage)
	}
	accountClaims.Limits.AccountLimits.Conn = limitValue(account.MaxConnections)
	accountClaims.Limits.NatsLimits.Subs = limitValue(account.MaxSubscriptions)
	accountClaims.Limits.NatsLimits.Data = limitValue(account.MaxData)
	accountClaims.Limits.NatsLimits.Payload = limitValue(account.MaxPayload)
}

// applyAccountExports converts export records to NATS JWT exports and adds them to account claims.
func (g *Generator) applyAccountExports(accountClaims *jwt.AccountClaims, exports []*pbtypes.AccountExportRecord) {
	for _, export := range exports {
		e := &jwt.Export{
			Name:                 export.Name,
			Subject:              jwt.Subject(export.Subject),
			TokenReq:             export.TokenReq,
			AccountTokenPosition: uint(export.AccountTokenPosition),
			Advertise:            export.Advertise,
			AllowTrace:           export.AllowTrace,
		}

		switch export.Type {
		case "service":
			e.Type = jwt.Service
		default:
			e.Type = jwt.Stream
		}

		if e.Type == jwt.Service {
			switch export.ResponseType {
			case "Stream":
				e.ResponseType = jwt.ResponseTypeStream
			case "Chunked":
				e.ResponseType = jwt.ResponseTypeChunked
			default:
				e.ResponseType = jwt.ResponseTypeSingleton
			}
			if export.ResponseThreshold > 0 {
				e.ResponseThreshold = time.Duration(export.ResponseThreshold) * time.Millisecond
			}
		}

		if export.Description != "" {
			e.Info.Description = export.Description
		}

		accountClaims.Exports.Add(e)
	}
}

// applyAccountImports converts import records to NATS JWT imports and adds them to account claims.
func (g *Generator) applyAccountImports(accountClaims *jwt.AccountClaims, imports []*pbtypes.AccountImportRecord) {
	for _, imp := range imports {
		i := &jwt.Import{
			Name:       imp.Name,
			Subject:    jwt.Subject(imp.Subject),
			Account:    imp.Account,
			Token:      imp.Token,
			Share:      imp.Share,
			AllowTrace: imp.AllowTrace,
		}

		switch imp.Type {
		case "service":
			i.Type = jwt.Service
		default:
			i.Type = jwt.Stream
		}

		if imp.LocalSubject != "" {
			i.LocalSubject = jwt.RenamingSubject(imp.LocalSubject)
		}

		accountClaims.Imports.Add(i)
	}
}

// applyAccountRevocations embeds the account's user-key revocations into the JWT.
// Each entry revokes all user JWTs for that public key issued at or before the
// stored cutoff. The list is carried in the account JWT and enforced by the NATS
// server when a user connects.
func (g *Generator) applyAccountRevocations(accountClaims *jwt.AccountClaims, account *pbtypes.AccountRecord) error {
	revocations, err := account.GetRevocations()
	if err != nil {
		return err
	}
	for pubKey, ts := range revocations {
		accountClaims.RevokeAt(pubKey, time.Unix(ts, 0))
	}
	return nil
}

// isSystemAccount reports whether this record is the NATS system account.
func (g *Generator) isSystemAccount(account *pbtypes.AccountRecord) bool {
	return g.systemAccountID != "" && account.ID == g.systemAccountID
}

// isSystemUser checks if a user belongs to the system account and requires special permissions.
func (g *Generator) isSystemUser(user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord) bool {
	return g.isSystemAccount(account) && user.NatsUsername == "sys"
}

// applyPermissions merges role-based and per-user permissions into user JWT claims.
// Per-user permissions are unioned with role permissions (additive).
//
// PERMISSION EVALUATION ORDER (NATS semantics):
// 1. Check if subject matches any Allow pattern
// 2. If allowed, check if subject matches any Deny pattern
// 3. Deny takes precedence over Allow for matching subjects
func (g *Generator) applyPermissions(userClaims *jwt.UserClaims, user *pbtypes.NatsUserRecord, account *pbtypes.AccountRecord, role *pbtypes.RoleRecord) error {
	// Get role allow permissions
	publishPerms, err := role.GetPublishPermissions()
	if err != nil {
		return fmt.Errorf("failed to get role publish permissions: %w", err)
	}
	subscribePerms, err := role.GetSubscribePermissions()
	if err != nil {
		return fmt.Errorf("failed to get role subscribe permissions: %w", err)
	}

	// Get role deny permissions
	publishDenyPerms, err := role.GetPublishDenyPermissions()
	if err != nil {
		return fmt.Errorf("failed to get role publish deny permissions: %w", err)
	}
	subscribeDenyPerms, err := role.GetSubscribeDenyPermissions()
	if err != nil {
		return fmt.Errorf("failed to get role subscribe deny permissions: %w", err)
	}

	// Get per-user permission overrides
	userPubPerms, err := user.GetPublishPermissions()
	if err != nil {
		return fmt.Errorf("failed to get user publish permissions: %w", err)
	}
	userSubPerms, err := user.GetSubscribePermissions()
	if err != nil {
		return fmt.Errorf("failed to get user subscribe permissions: %w", err)
	}
	userPubDenyPerms, err := user.GetPublishDenyPermissions()
	if err != nil {
		return fmt.Errorf("failed to get user publish deny permissions: %w", err)
	}
	userSubDenyPerms, err := user.GetSubscribeDenyPermissions()
	if err != nil {
		return fmt.Errorf("failed to get user subscribe deny permissions: %w", err)
	}

	// Merge: union of role + user permissions
	publishPerms = append(publishPerms, userPubPerms...)
	subscribePerms = append(subscribePerms, userSubPerms...)
	publishDenyPerms = append(publishDenyPerms, userPubDenyPerms...)
	subscribeDenyPerms = append(subscribeDenyPerms, userSubDenyPerms...)

	// Apply default permissions if both role and user permissions are empty
	if len(publishPerms) == 0 {
		if g.isSystemUser(user, account) {
			publishPerms = []string{"$SYS.>", ">"}
		} else {
			publishPerms = g.options.DefaultPublishPermissions
		}
	}
	if len(subscribePerms) == 0 {
		if g.isSystemUser(user, account) {
			subscribePerms = []string{"$SYS.>", ">"}
		} else {
			subscribePerms = g.options.DefaultSubscribePermissions
		}
	}

	// Apply allow permissions
	for _, perm := range publishPerms {
		userClaims.Permissions.Pub.Allow.Add(perm)
	}
	for _, perm := range subscribePerms {
		userClaims.Permissions.Sub.Allow.Add(perm)
	}

	// Apply deny permissions (these take precedence over allow)
	for _, perm := range publishDenyPerms {
		userClaims.Permissions.Pub.Deny.Add(perm)
	}
	for _, perm := range subscribeDenyPerms {
		userClaims.Permissions.Sub.Deny.Add(perm)
	}

	// Apply response permissions for request-reply patterns
	if role.AllowResponse {
		userClaims.Permissions.Resp = &jwt.ResponsePermission{}

		// Set max responses (-1 = unlimited, 0 or unset = default of 1)
		if role.AllowResponseMax == -1 {
			userClaims.Permissions.Resp.MaxMsgs = jwt.NoLimit
		} else if role.AllowResponseMax > 0 {
			userClaims.Permissions.Resp.MaxMsgs = role.AllowResponseMax
		} else {
			// Default: 1 response allowed
			userClaims.Permissions.Resp.MaxMsgs = 1
		}

		// Set response TTL (0 = no limit/default)
		if role.AllowResponseTTL > 0 {
			userClaims.Permissions.Resp.Expires = time.Duration(role.AllowResponseTTL) * time.Second
		}
		// If TTL is 0, leave Expires as zero value (no expiration)
	}

	return nil
}

// applyRoleLimits applies role-based per-user limits to user JWT claims.
func (g *Generator) applyRoleLimits(userClaims *jwt.UserClaims, role *pbtypes.RoleRecord) {
	userClaims.Limits.Data = limitValue(role.MaxData)
	userClaims.Limits.Subs = limitValue(role.MaxSubscriptions)
	userClaims.Limits.Payload = limitValue(role.MaxPayload)
}

// GenerateSystemAccountJWT generates a specialized JWT for the system account (SYS).
func (g *Generator) GenerateSystemAccountJWT(sysAccount *pbtypes.AccountRecord, operatorSigningSeed string) (string, error) {
	operatorKP, err := g.nkeyManager.KeyPairFromSeed(operatorSigningSeed)
	if err != nil {
		return "", fmt.Errorf("failed to create operator signing key pair: %w", err)
	}

	accountClaims := jwt.NewAccountClaims(sysAccount.PublicKey)
	accountClaims.Name = "SYS"
	for _, pubKey := range sysAccount.AllSigningPublicKeys() {
		accountClaims.SigningKeys.Add(pubKey)
	}

	// The system account carries revocations like any other: suspending, revoking
	// or deleting a user in it regenerates its JWT to embed the cutoff, and that
	// regeneration now lands here rather than on the ordinary account path.
	if err := g.applyAccountRevocations(accountClaims, sysAccount); err != nil {
		return "", fmt.Errorf("failed to apply system account revocations: %w", err)
	}

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

	// Disable JetStream for system account
	accountClaims.Limits.JetStreamLimits.DiskStorage = 0
	accountClaims.Limits.JetStreamLimits.MemoryStorage = 0
	accountClaims.Limits.JetStreamLimits.Streams = 0
	accountClaims.Limits.JetStreamLimits.Consumer = 0

	// Unlimited core NATS limits for system operations
	accountClaims.Limits.AccountLimits.Conn = jwt.NoLimit
	accountClaims.Limits.NatsLimits.Subs = jwt.NoLimit
	accountClaims.Limits.NatsLimits.Data = jwt.NoLimit
	accountClaims.Limits.NatsLimits.Payload = jwt.NoLimit

	jwtValue, err := accountClaims.Encode(operatorKP)
	if err != nil {
		return "", fmt.Errorf("failed to encode system account JWT: %w", err)
	}

	return jwtValue, nil
}
