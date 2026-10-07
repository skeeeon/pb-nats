# Changelog

## v0.3.0

### Breaking: an empty permission list now grants nothing

Before this release, when a role and a user together had no publish subjects, pb-nats signed the user JWT with `DefaultPublishPermissions` (`>`). When they had no subscribe subjects, it used `DefaultSubscribePermissions` (`>`, `_INBOX.>`). So a role saved with blank lists produced users that could publish and subscribe to everything in their account, for example any device command subject or any KV bucket. The account boundary still held, so other tenants were not exposed.

Setting the options to an empty list did not turn this off, for two reasons:

- `Setup` replaced an empty option with the `>` defaults.
- nats-server treats an empty allow list in a user JWT as **no restriction**. A JWT with an empty list was therefore just as open.

Now, if the role and the user together grant no subjects in a direction, the JWT gets `deny: [">"]` for that direction. nats-server rejects every publish or subscribe in that direction. Publish and subscribe are handled separately. A role with only `subscribe_permissions` gives a subscribe-only user. `allow_response` still works under the deny, so a role with subscribe permissions, no publish permissions and `allow_response: true` is a reply-only service. `_INBOX.>` is not added for you. A role that makes requests must list it.

The `sys` user in the system account is unchanged. It still falls back to `$SYS.>` and `>`, because it is pb-nats' own connection.

**API removals.** These are removed so that code which configured the old fallback fails to compile instead of silently changing behaviour:

- `Options.DefaultPublishPermissions`
- `Options.DefaultSubscribePermissions`
- the package variables `pbnats.DefaultPublishPermissions` and `pbnats.DefaultSubscribePermissions`

If you set them, decide what each affected role should grant and write that into the role (see below).

`pbnats.Version` now reports `0.3.0`. It said `1.3.0`, which matched no release.

### Upgrade notes

**1. Before upgrading, find the users this affects.** A user is affected when the role and the user *together* have no subjects in a direction. A per-user list fills an empty role. Run this against `pb_data/data.db` (SQLite; adjust the table names if you changed the collection names):

```sql
-- Active users whose next JWT will deny all publishing and/or all subscribing.
-- Empty means NULL, '', 'null' or '[]': every form pb-nats parses as "no subjects".
SELECT u.id, u.nats_username, u.account_id, r.name AS role, r.is_default,
       pub.n = 0 AS loses_publish,
       sub.n = 0 AS loses_subscribe
FROM nats_users u
JOIN nats_roles r ON r.id = u.role_id
JOIN (SELECT u2.id,
             COALESCE(json_array_length(NULLIF(r2.publish_permissions, '')), 0)
           + COALESCE(json_array_length(NULLIF(u2.publish_permissions, '')), 0) AS n
      FROM nats_users u2 JOIN nats_roles r2 ON r2.id = u2.role_id) pub ON pub.id = u.id
JOIN (SELECT u2.id,
             COALESCE(json_array_length(NULLIF(r2.subscribe_permissions, '')), 0)
           + COALESCE(json_array_length(NULLIF(u2.subscribe_permissions, '')), 0) AS n
      FROM nats_users u2 JOIN nats_roles r2 ON r2.id = u2.role_id) sub ON sub.id = u.id
WHERE u.active = 1
  AND u.nats_username != 'sys'   -- the system user keeps full access
  AND (pub.n = 0 OR sub.n = 0);
```

To list the roles instead, for example to check `is_default` roles that have no users yet:

```sql
SELECT id, name, is_default, publish_permissions, subscribe_permissions
FROM nats_roles
WHERE COALESCE(json_array_length(NULLIF(publish_permissions, '')), 0) = 0
   OR COALESCE(json_array_length(NULLIF(subscribe_permissions, '')), 0) = 0;
```

**2. Decide what each affected role should grant, and write it into the role.**

- If the role really needed full access, set `publish_permissions: [">"]` and `subscribe_permissions: [">", "_INBOX.>"]`. Behaviour is then unchanged, and the grant is visible on the role.
- Otherwise, list the subjects the role needs, or leave the list empty to grant nothing.

Saving a role reissues the JWTs of its active users.

**3. Upgrade.** Nothing is reissued at startup. A user gets a JWT under the new rules on the next event that reissues it: a role edit, a user edit, `regenerate`, `revoke`, or reactivation.

**4. Revoke where the old grant must stop now.** A JWT minted before the upgrade still carries `>`. NATS keeps accepting it until it expires or its key is revoked, and with `DefaultJWTExpiry: 0` it never expires. A reissue does **not** invalidate it, because `regenerate` keeps the same key pair. For a user who should not have had full access, set `{"revoke": true}`. This rotates the key pair, revokes the old key on the account, and issues new credentials, which must then be redistributed to the client.

**5. Check the result against the server, not just the role.** Connect with an affected user's new credentials. Confirm that a subject outside its grant is rejected with a permissions violation, and that a subject inside its grant still works. A test that only checks the denial also passes against a broken connection.
