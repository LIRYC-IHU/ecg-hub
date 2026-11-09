# Multi-tenant mode — design

Status: draft, nothing implemented. Target: a homelab cluster first, no real
patient data until the isolation tests in §11 pass.

## 1. Goal and scope

One ECG Hub deployment serves several independent practices ("tenants"). Each
practice sees only its own patients, ECGs, users, tags and audit trail. A
practice can be removed completely.

The mode is **off by default**. A deployment that does not enable it behaves
exactly as today: every row belongs to one built-in default tenant, no Row
Level Security policy exists, every feature stays available.

```yaml
tenancy:
  enabled: false        # true = multi-tenant mode
```

Out of scope for the first version: tenant-specific roles, tenant-specific
module/HL7/connector configuration, one database per tenant.

## 2. What the code does today

These facts drive the design; each one is a cross-tenant leak once several
practices share a database.

| Today | Consequence with several tenants |
|---|---|
| `patients.patient_id` is globally unique (`models/patient.go`) | Two practices numbering patients the same way merge their patients. |
| `ecgs.content_hash` is globally unique; `Persister` drops a file whose hash exists (`ingestion/persister.go:266`) | Uploading a file another practice already has fails silently and reveals it exists. |
| `ecgs.patient_id`, `user_pins`, `patient_tags` reference `patients(patient_id)` | Foreign keys must become `(tenant_id, patient_id)`. |
| The realtime hub broadcasts every event to every connected client (`events/hub.go`) | Practice A sees practice B's ingestion notifications. |
| The webhook dispatcher fans out to every enabled webhook (`webhook/dispatcher.go:303`) | Practice B's webhook receives practice A's ECG events. |
| Module, HL7, connector, auth and branding settings are single global rows | They cannot be split per tenant without a model change. |
| Background jobs (uploader, janitor, HL7/connector retry, export pool, webhook pruning) query with no request context | They have no tenant to scope with. |
| The ECG Hub JWT carries `sub` and `role` only (`auth/token.go`) | The tenant must come from somewhere else. |

## 3. Tenant model

```go
// models/tenant.go
type Tenant struct {
    ID        string    `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
    Slug      string    `gorm:"type:text;not null;uniqueIndex"` // matches the IdP organization alias
    Name      string    `gorm:"type:text;not null"`
    CreatedAt time.Time `gorm:"autoCreateTime"`
}
```

The default tenant has a fixed id, `00000000-0000-0000-0000-000000000001`,
slug `default`, created by `RunMigrations`. A fixed id lets every
`tenant_id` column carry it as its SQL `DEFAULT`, so existing rows and every
code path that does not set a tenant yet land in the default tenant.

## 4. Data

### Tables that get `tenant_id uuid NOT NULL DEFAULT <default> REFERENCES tenants(id) ON DELETE CASCADE`

`patients`, `ecgs`, `quarantine_entries`, `ecg_buffer`, `tags`,
`patient_tags`, `ecg_tags`, `user_pins`, `ecg_hub_users`, `api_keys`,
`user_webhooks`, `webhook_deliveries`, `export_jobs`, `export_job_ecgs`,
`connector_jobs`, `hl7_attempts`, `hl7_oru_attempts`, `nihon_kohden_transfers`.

Rows hanging off a user (`api_keys`, `user_webhooks`, `export_jobs`) already
cascade from `ecg_hub_users`; they still get the column so RLS can filter them
without a join.

`audit_logs` gets `tenant_id` **without** the cascade, see §9.

### Tables that stay global (platform configuration)

`roles`, `role_permissions`, `module_configs`, `module_settings`,
`hl7_settings`, `hl7_mapping_presets`, `hl7_mappings`, `hl7_inbound_messages`,
`auth_provider_configs`, `local_users`, `devices`, `tenants`.

### Uniqueness and keys that change

| Constraint today | Becomes |
|---|---|
| `patients(patient_id)` unique | `patients(tenant_id, patient_id)` unique |
| `ecgs(content_hash)` unique | `ecgs(tenant_id, content_hash)` unique |
| `ecgs.patient_id → patients.patient_id` | `(tenant_id, patient_id) → patients(tenant_id, patient_id)` |
| `user_pins.patient_id`, `patient_tags.patient_id` → `patients.patient_id` | composite, same as above |
| `quarantine_entries.content_hash` index | `(tenant_id, content_hash)` |

The composite foreign keys go in the `fkFix` list of `db/migrate.go`, next to
the ones AutoMigrate already cannot generate. `ExistsByContentHash` and
`FindByContentHash` take the tenant.

## 5. Isolation: Row Level Security

The application filters by tenant, and Postgres enforces it. A query that
forgets the filter returns nothing instead of another practice's rows.

Created by `RunMigrations` only when `tenancy.enabled` is true, for every
table in the first list of §4:

```sql
ALTER TABLE ecgs ENABLE ROW LEVEL SECURITY;
ALTER TABLE ecgs FORCE ROW LEVEL SECURITY;   -- the app role owns the tables: without FORCE it bypasses RLS
CREATE POLICY tenant_isolation ON ecgs
  USING      (tenant_id = current_setting('app.tenant_id', true)::uuid
              OR current_setting('app.system', true) = 'on')
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid
              OR current_setting('app.system', true) = 'on');
```

`current_setting(..., true)` returns NULL when the setting is absent, so an
unscoped query matches no row and an unscoped insert is rejected.

### Setting the tenant on a pooled connection

`SET LOCAL` only lives inside a transaction, and the pool hands out a
different connection per statement. Every scoped database access therefore
runs in a short transaction:

```go
// internal/tenancy/tx.go
func Tx(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
    t, ok := FromContext(ctx)
    if !ok {
        return ErrNoTenant
    }
    return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
        if err := tx.Exec(`SELECT set_config('app.tenant_id', ?, true)`, t.ID).Error; err != nil {
            return err
        }
        return fn(tx)
    })
}
```

Repository methods wrap their body in `tenancy.Tx`; handler signatures do not
change. The 21 direct `h.DB` uses in six handler files move into repositories
or wrap themselves. When tenancy is off, `Tx` still sets the default tenant,
so there is one code path.

Cost: a `BEGIN`, a `set_config` and a `COMMIT` per repository call. Measurable
only at a request rate far above a practice platform.

### System context

Background jobs work across tenants. They run with `tenancy.System(ctx)`,
which sets `app.system = on` instead of a tenant, and they **must carry the
row's `tenant_id` forward** in anything they emit (event, webhook, export).
`app.system` is never derived from a request. Ceiling: one database role; a
dedicated `BYPASSRLS` role for system jobs is the upgrade if an audit asks for
it.

## 6. Request path

1. **Login (OIDC).** A new `tenant_claim` setting in the OIDC configuration,
   next to `groups_claim`. With Keycloak 26 Organizations the claim is
   `organization` and holds the organization alias. `ExchangeAndIssue` maps
   it to `tenants.slug`. No claim, an unknown slug, or several organizations
   refuses the login. Tenants are never created on the fly by a login.
2. **User record.** `ecg_hub_users.tenant_id` is set at first login and
   checked at every login: a user whose claim changes tenant is refused and
   logged, not moved.
3. **Each request.** The auth middleware resolves the tenant from the user
   record, with a short cache like `PermissionChecker`'s, and puts it in the
   context. The tenant is **not** added to the ECG Hub JWT: the database
   stays the single source, and the sliding refresh (`IssueToken(sub, role)`)
   does not change.
4. **API keys** resolve to their user, hence to the user's tenant.

The tenant must never come from an attribute users can edit themselves (a
Keycloak user attribute editable from the account console). Organization or
group membership is admin-only.

Local accounts remain for the platform administrator. LDAP is refused at
startup in tenant mode for the first version (one directory, no tenant
mapping defined).

## 7. Ingestion and storage

- `IngestItem` gets a `TenantID`. The upload handler (`handlers/uploads.go`)
  sets it from the uploading user. FTP, DICOM and ECTP listeners set the
  default tenant (see §8).
- The `Persister` writes `tenant_id` on the ECG, quarantine and buffer rows
  and deduplicates per tenant.
- Local layout becomes `<root>/<tenant_id>/<patient_id>/<file>`. The S3 key
  follows, since `objectKey` keeps the path relative to the root
  (`storage/uploader.go:222`). Deleting a tenant's files is one prefix.

## 8. Events, webhooks, and features held back

- `events.Event` gets `TenantID`. A realtime subscriber registers with its
  tenant and only receives matching events. Same in the device event stream.
- The webhook dispatcher only delivers to webhooks whose `tenant_id` matches
  the event's.

Disabled in tenant mode for the first version, because their configuration is
global and they would leak or misroute data:

| Feature | Why |
|---|---|
| HL7 ADT inbound, ORU outbound, MLLP listener | One HIS feed and one destination for the whole deployment. |
| PACS connectors | One connector set forwards every ECG. |
| FTP / DICOM / ECTP for non-default tenants | A listener cannot tell which practice a device belongs to. |
| Device whitelist | Same reason. |

Each one comes back with a per-tenant binding when a practice needs it.
They stay fully available when tenancy is off.

## 9. Administration and tenant removal

- New permission `tenant.manage`, held only by the platform administrator
  role. It covers creating, listing and deleting tenants.
- The `admin.roles`, `admin.system`, `admin.auth_config` and `admin.branding`
  permissions stay platform-wide. A practice administrator gets a role with
  `admin.users` only, which RLS limits to the practice's own users.

Removal of a practice:

1. Export of all its ECGs to the practice (existing export jobs, system context).
2. `DELETE FROM tenants WHERE id = ?`. The foreign keys cascade through
   every table of §4.
3. Deletion of `<tenant_id>/` in local storage and in the bucket.
4. One `tenant.deleted` audit entry.

`audit_logs` rows are **kept** with their `tenant_id` and no foreign key:
traceability must outlive the practice. Whether they are later purged or
pseudonymised is an open question (§13).

## 10. Migration and switching

- Schema changes go through `RunMigrations`, as today. AutoMigrate adds each
  `tenant_id` column with its `DEFAULT`, so an existing database fills every
  row with the default tenant on the next start.
- RLS policies are created when `tenancy.enabled` is true, and dropped when
  it is false and only the default tenant exists.
- Startup refuses `tenancy.enabled: false` when tenants other than the
  default exist: turning the mode off would expose them to each other.

## 11. Tests

- **Isolation suite** (Postgres, `TEST_DATABASE_URL`): two tenants with the
  same `patient_id` and the same file. Every list, get, search, download,
  update and delete endpoint, as a user of each tenant, sees only its own.
- **RLS without application filter:** a raw query in tenant context returns
  only that tenant's rows; one without context returns none; an insert
  without context fails.
- **Events:** a realtime subscriber and a webhook of tenant B receive nothing
  from an upload in tenant A.
- **Removal:** after deleting a tenant, no row with its id remains outside
  `audit_logs`, and its storage prefix is empty.
- **Mode off:** the existing suite passes unchanged.

## 12. Work plan

Each step is one pull request, mergeable with the mode off.

1. `tenants` table, default tenant, `tenant_id` columns, composite keys,
   per-tenant dedup. No behaviour change.
2. `internal/tenancy` (context, `Tx`, `System`), repositories wrapped, direct
   handler DB uses removed.
3. RLS policies behind `tenancy.enabled`, the RLS tests.
4. OIDC `tenant_claim`, tenant on the user record, middleware.
5. Ingestion, storage layout, events and webhooks scoped.
6. Features held back in tenant mode, `tenant.manage`, tenant removal.
7. The isolation suite against a homelab deployment with Keycloak Organizations.

## 13. Open questions

- Retention of a removed practice's audit entries: keep, purge after a delay,
  or pseudonymise.
- How practices send ECGs beyond the web upload: client tool, API key, or a
  module bound to a tenant.
- Whether a practice administrator may create its own roles.
- Patient identifier for practices: their own numbering, or the national
  health identifier, which needs its own qualification workflow.
