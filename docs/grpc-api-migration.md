# Migration API REST → gRPC/Connect

Branche `feature/grpc`. Pilote **healthz** livré (back + front). Ce document liste **toutes les routes** à migrer, en étapes, de la plus simple à la plus risquée.

## Conventions

- **Statut** : `[x]` fait · `[ ]` à faire · `[~]` en cours
- **Type gRPC** :
  - `unary` — requête/réponse simple (la majorité)
  - `server-stream` — remplace un WebSocket ou un flux (progression, events)
  - `bytes/stream` — download/upload binaire (à évaluer : garder REST ou streamer)
  - `REST-only` — flux navigateur (redirect OIDC, cookie) : **ne se migre pas** en gRPC
- **Package proto** : `grpc.api.v1` · chemin wire `/grpc.api.v1.<Service>/<Method>`
- Chaque service : interceptors `validate` (protovalidate) + auth. Validation déclarée dans le `.proto` via `buf/validate/validate.proto`.

## Briques déjà en place

- [x] Transport h2c backend + nginx `grpc_pass` (+ `location /api/` pour Connect)
- [x] Interceptor JWT (`ConnectOptionalAuth`) + interceptor validation (`connectrpc.com/validate`)
- [x] Génération Go (`buf generate`) + TS connect-web (`frontend/src/gen`)
- [x] Client frontend typé (`frontend/src/lib/grpc.ts`) — un `createClient` par service
- [x] Helper `mountConnect(e, path, handler)` (router.go) — double montage `/api`+racine, réutilisé par chaque service
- [x] Interceptors : `ConnectOptionalAuth` (JWT optionnel), `ConnectRequireAuth` (bloquant, JWT+clé API), `ConnectRequirePermission(checker, map procédure→perm)` (par méthode)
- [x] Pattern rodé : proto `v1/<svc>.proto` → `buf generate` (Go + TS) → `handlers/<svc>_service.go` → `mountConnect` (+ interceptors auth/perm si protégé) → client front → retrait REST

---

## Étape 0 — Pilote (FAIT)

- [x] `GET /healthz` → `HealthzService.CheckHealth` — `unary` — payload public/authentifié
  - reste : retirer le REST `/healthz` (backend + nginx ×3 + tests + swagger) — ⚠️ vérifier health-check prod avant

---

## Étape 1 — Auth & bootstrap (public, sensible)

Service `AuthService` + `SetupService`. Attention aux flux cookie/redirect.

- [x] `GET  /api/v1/branding` → `BrandingService.GetBranding` — `unary` — public ✅ (REST retiré)
- [x] `GET  /api/v1/setup/status` → `SetupService.GetStatus` — `unary` — public ✅ (REST retiré)
- [x] `POST /api/v1/setup` → `SetupService.Initialize` — `unary` — public ✅ (REST retiré ; validation → InvalidArgument, déjà initialisé → FailedPrecondition ; logique partagée `createFirstAdmin`)
- [x] `GET  /api/v1/auth/provider` → `AuthService.GetProviders` — `unary` — public ✅ (REST retiré)
- [x] `POST /api/v1/auth/login` — **REST-only** (garde le cookie httpOnly + rate-limit + throttle + LDAP + audit ; peu fréquent, décision Jonathan)
- [x] `GET  /api/v1/auth/logout` — **REST-only** (efface cookie + redirect OIDC end-session)
- [ ] `GET  /api/v1/auth/oidc/login` — **REST-only** (redirect navigateur)
- [ ] `GET  /api/v1/auth/oidc/callback` — **REST-only** (redirect navigateur)
- [x] `GET  /api/v1/auth/me` → `SessionService.GetCurrentUser` — `unary` ✅ (REST retiré) — **interceptor auth bloquant `ConnectRequireAuth` (JWT cookie/Bearer + clé API) créé, réutilisable par toutes les routes protégées**

## Étape 2 — Patients & timeline (lecture, gros volume front)

Service `PatientService`.

- [x] `GET  /patients` → `PatientService.Search` — `unary` — patient.read ✅ (REST retiré ; message `Patient` + 12 params + agrégats ECG ; `patients.go` supprimé)
- [x] `GET  /patients/:id/ecgs` → `PatientService.ListECGs` — `unary` — patient.read ✅ (REST retiré ; message `Ecg` partagé + mapper `ecgToProto`)
- [x] `POST /patients/:id/ecgs/view` → `PatientService.MarkECGsViewed` — `unary` — ecg.read ✅ (REST retiré)
- [x] `GET  /ecgs` → `ECGService.ListAll` — `unary` — patient.read ✅ (REST retiré ; message `EcgWithPatient` = `Ecg` composé + démographie)
- [x] `GET  /ecgs/filters` → `ECGService.GetFilters` — `unary` — patient.read ✅ (REST retiré ; 1re route protégée+permission)

## Étape 3 — ECG détail & métadonnées

Service `ECGService`.

- [x] `GET    /ecgs/:id/metadata` → `ECGService.GetMetadata` — `unary` — ecg.read ✅ (REST retiré ; `fields` + `values_json`)
- [x] `PATCH  /ecgs/:id/metadata` → `ECGService.UpdateMetadata` — `unary` — ecg.write ✅ (REST retiré ; `values_json` patch)
- [x] `POST   /ecgs/:id/view` → `ECGService.MarkViewed` — `unary` — ecg.read ✅ (REST retiré ; idempotent)
- [x] `DELETE /ecgs/:id` → **REST-only** (décision Jonathan 15/07) — ecg.delete
- [x] `GET    /ecgs/:id/waveform` → **REST-only** (décision 15/07 ; binaire DICOM `c.Blob`, pas de gain gRPC) — ecg.read
- [x] `GET    /ecgs/:id/download` → **REST-only** (décision 15/07 ; ⚠️ gros fichiers + zip batch, `c.Attachment`/`Content-Disposition`, base64/mémoire à éviter) — ecg.download

> **Règle de partage figée (15/07)** : gRPC/Connect = tout le JSON métier typé · REST = binaire (waveform, download, export/upload/logo) + flux cookie/OIDC (login, logout, callback) + DELETE ECG. L'auth reste identique (mêmes `AuthMiddleware`/`RequirePermission` lisant le cookie/JWT que les interceptors gRPC). Réévaluer le binaire **seulement** si un besoin concret de streaming progressif apparaît (→ server-stream Connect, mais perte du download navigateur natif).

## Étape 4 — HL7 (par ECG)

- [x] `POST /ecgs/:id/hl7/force` → `HL7Service.Force` — `unary` — ecg.force_hl7 ✅ (REST retiré)
- [x] `GET  /ecgs/:id/oru-status` → `HL7Service.GetOruStatus` — `unary` — ecg.read ✅ (REST retiré ; attempt optionnel)
- [x] `POST /ecgs/:id/send-result` → `HL7Service.SendResult` — `unary` — ecg.send_result ✅ (REST retiré ; guards→FailedPrecondition, échec envoi→Unavailable, ORU nil→FailedPrecondition ; test porté)
- [x] `GET  /patients/:id/hl7-history` → `HL7Service.ListAttempts` — `unary` — patient.read ✅ (REST retiré)

## Étape 5 — Tags & pins (petit, rapide, bon entraînement CRUD)

Service `TagService` + `PinService`.

**`TagService` FAIT ✅ (REST retiré) — 12 RPC dont 2 batch qui tuent le N+1 (50 lignes = 1 requête au lieu de 50) :**
- [x] `GET    /tags` → `TagService.ListTags` — patient.read
- [x] `POST   /tags` → `TagService.CreateTag` — tag.create
- [x] `PUT    /tags/:id` → `TagService.UpdateTag` — tag.create
- [x] `DELETE /tags/:id` → `TagService.DeleteTag` — tag.delete
- [x] `GET    /patients/:id/tags` → `TagService.ListPatientTags` — patient.read
- [x] `POST   /patients/:id/tags` → `TagService.TagPatient` — tag.apply
- [x] `DELETE /patients/:id/tags/:tag_id` → `TagService.UntagPatient` — tag.apply
- [x] `GET    /ecgs/:id/tags` → `TagService.ListEcgTags` — patient.read
- [x] `POST   /ecgs/:id/tags` → `TagService.TagEcg` — tag.apply
- [x] `DELETE /ecgs/:id/tags/:tag_id` → `TagService.UntagEcg` — tag.apply
- [x] **NOUVEAU** `TagService.BatchGetPatientTags(ids[]) → map<id,TagList>` — patient.read (batch liste patients)
- [x] **NOUVEAU** `TagService.BatchGetEcgTags(ids[]) → map<id,TagList>` — patient.read (batch liste ECG)
- [x] `GET    /pins` → `PinService.ListPins` — patient.read ✅ (REST retiré ; identité via contexte)
- [x] `POST   /pins` → `PinService.PinPatient` — patient.read ✅ (REST retiré)
- [x] `DELETE /pins/:patient_id` → `PinService.UnpinPatient` — patient.read ✅ (REST retiré)

## Étape 6 — Upload & exports (binaire + streaming)

- [x] `POST /uploads` → **REST-only** (décision Jonathan 15/07) — ecg.upload. Multipart, browser natif (progress gratuit, pas de base64). **connect-web NE PEUT PAS client-streamer** (HTTP/1.1 = unary + server-stream only) → un upload RPC serait inutilisable par le front. Statut live par fichier = déjà via WebSocket → EventService.Subscribe.
- [x] `POST /exports` → `ExportService.Create` — `unary` — ecg.download ✅ (REST retiré ; unary+stream interceptors coexistent)
- [x] `POST /exports/formats` → `ExportService.Formats` — `unary` — ecg.download ✅ (REST retiré)
- [x] `GET  /exports/:id` → `ExportService.Get` — `unary` — ecg.download ✅ (REST retiré ; ownership 404)
- [x] `GET  /exports/:id/ws` → `ExportService.WatchProgress` — `server-stream` — ecg.download ✅ (ex-WebSocket retiré ; ownership dans le handler ; `ConnectStreamAuth` ; download ZIP reste REST)
- [ ] `GET  /exports/:id/download` → `ExportService.Download` — `bytes/stream` — ecg.download (⚠️ ZIP)

## Étape 7 — Realtime events (WebSocket → streaming)

- [x] `GET /events/ws` → `EventService.Subscribe` — `server-stream` — patient.read ✅ (ex-WebSocket retiré ; 1er streaming ; keepalive immédiat = "ready" edge ; interceptor streaming `ConnectStreamAuth`)

## Étape 8 — Admin : users, rôles, quarantaine, audit, stats

Service `AdminService` (un seul service, permission par procédure). Tout `admin.*`.
✅ Backend gRPC + front migrés, routes REST retirées, build back + tsc front verts.
⚠️ Anciens handlers REST laissés en code mort (Go l'autorise) — nettoyage à faire.

- [x] `GET    /audit-logs` → `AdminService.ListAuditLogs` — admin.audit ✅ (details JSONB → `details_json` string ; enrichissement username best-effort)
- [x] `GET    /admin/stats` → `AdminService.GetStats` — admin.system ✅
- [x] `GET    /admin/storage-metrics` → `AdminService.GetStorageMetrics` — admin.system ✅
- [x] `GET    /admin/errors` → `AdminService.GetRecentErrors` — admin.system ✅
- [ ] `GET    /admin/users` · `PUT /admin/users/:id/role` — **REST-only** (Keycloak legacy vs config DB-driven ; laissé REST comme login/logout)
- [x] `GET    /admin/app-users` · `PUT /admin/app-users/:id/role` · `DELETE /admin/app-users/:id` → `AdminService.{ListAppUsers,SetAppUserRole,DeleteAppUser}` — admin.users ✅ (self-delete → PermissionDenied, dernier local → FailedPrecondition)
- [x] `GET/POST/PUT/DELETE /admin/roles[...]` → `AdminService.{ListRoles,CreateRole,UpdateRole,DeleteRole}` — admin.roles ✅ (garde-fou dernier admin.roles → FailedPrecondition, rôle utilisé → FailedPrecondition)
- [x] `GET    /admin/quarantine` · `DELETE /admin/quarantine/:id` · `POST /admin/quarantine/:id/assign` → `AdminService.{ListQuarantine,DeleteQuarantine,AssignQuarantine}` — quarantine.* ✅ (assign re-ingère le fichier, garde anti wrong-patient `create_new`)
- [x] `GET    /admin/settings/user-defaults` · `PUT ...` → `AdminService.{GetUserDefaults,SetUserDefaults}` — admin.roles ✅
- [ ] `PUT    /admin/settings/branding` · `POST /admin/settings/branding/logo` — admin.branding (⚠️ upload logo = bytes → reste REST ; le save JSON pourra passer en gRPC)

## Étape 9 — Admin : modules & connecteurs (hot-control)

Service `ModuleService` (un seul service, tout `admin.system`).
✅ Backend gRPC + front migrés, routes REST retirées, build back + tsc front verts.
⚠️ Anciens handlers REST laissés en code mort (nettoyage à faire, mutualisé avec étape 8).

- [x] `GET  /modules` → `ModuleService.ListModules` — admin.system ✅ (status/version/formats)
- [x] `GET  /admin/modules/status` → `ModuleService.ListModuleStatus` — admin.system ✅
- [x] `POST /admin/modules/:name/stop` · `.../start` → `ModuleService.{StopModule,StartModule}` — admin.system ✅ (start ftp/dicom via helpers partagés `Start*FromDB`, inconnu → Unimplemented, arrêt module absent → NotFound)
- [x] `GET/PUT /admin/modules/ftp/config` → `ModuleService.{GetFTPConfig,SaveFTPConfig}` — admin.system ✅ (mot de passe masqué/préservé, validation port + passive range)
- [x] `GET/PUT /admin/modules/dicom/config` → `ModuleService.{GetDICOMConfig,SaveDICOMConfig}` — admin.system ✅
- [x] `GET/PUT /admin/settings/modules` → `ModuleService.{GetModuleSettings,SaveModuleSettings}` — admin.system ✅ (hot-reload router)
- [x] `GET  /admin/connectors` → `ModuleService.ListConnectors` — admin.system ✅ (health, met à jour la gauge Prometheus)
- [x] `GET /admin/connectors/config` → `ModuleService.ListConnectorConfigs` — admin.system ✅ (mots de passe masqués)
- [x] `PUT  /admin/connectors/:name/config` · `DELETE ...` · `POST .../test` → `ModuleService.{SaveConnectorConfig,DeleteConnector,TestConnector}` — admin.system ✅ (préserve ftp_password, reload runtime, test = dial TCP → success:false sur échec réseau)

## Étape 10 — Admin : HL7 config & presets

Service `HL7AdminService` (distinct de `HL7Service` per-ECG). Settings/scheduler peuvent être nil (HL7 off) → handler renvoie Unavailable.
✅ Backend gRPC + front migrés, routes REST retirées, build back + tsc front verts.
⚠️ Anciens handlers REST laissés en code mort (nettoyage à faire, mutualisé 8-10).

- [x] `GET/POST /admin/hl7/presets` · `.../:id/activate` · `PUT .../:id/mappings` · `DELETE .../:id` → `HL7AdminService.{ListPresets,CreatePreset,ActivatePreset,SavePresetMappings,DeletePreset}` — hl7.config ✅ (nom dupliqué → AlreadyExists ; mappings incomplets filtrés)
- [x] `GET  /admin/hl7/active-mappings` → `HL7AdminService.GetActiveMappings` — patient.read ✅
- [x] `POST /admin/hl7/test` · `POST /admin/hl7/ping` → `HL7AdminService.{TestQuery,Ping}` — hl7.config ✅ (client temporaire depuis settings DB ; arbre HL7 récursif → `tree_json` parsé côté front ; échec réseau = success:false, pas d'erreur gRPC)
- [x] `GET/PUT /admin/hl7/settings` → `HL7AdminService.{GetSettings,UpdateSettings}` — hl7.config ✅ (proto3 `optional` = sémantique PATCH partielle ; toutes les validations portées ; reload scheduler)
- [x] `POST /admin/hl7/run` → `HL7AdminService.ForceRun` — hl7.config · `POST /admin/hl7/bulk-retry` → `HL7AdminService.BulkRetry` — hl7.bulk_retry ✅

## Étape 11 — Admin : auth providers (OIDC/LDAP)

Service `AuthAdminService` (tout `admin.auth_config`). Config polymorphe (OIDC/LDAP) opaque côté UI → transportée en `config_json` (le backend parse/valide dans SaveOIDCRequest/SaveLDAPRequest, masque les secrets à la lecture).
✅ Backend gRPC + front migrés, routes REST retirées, build back + tsc front verts.
⚠️ Anciens handlers REST laissés en code mort (nettoyage à faire, mutualisé 8-11).

- [x] `GET    /admin/auth/providers` → `AuthAdminService.ListProviders` — admin.auth_config ✅ (secrets masqués)
- [x] `PUT    /admin/auth/oidc` · `PUT /admin/auth/ldap` → `AuthAdminService.{SaveOIDC,SaveLDAP}` — admin.auth_config ✅ (secret masqué/préservé, restart_required)
- [x] `DELETE /admin/auth/providers/:id` → `AuthAdminService.DeleteProvider` — admin.auth_config ✅
- [x] `POST   /admin/auth/oidc/test` · `POST /admin/auth/ldap/test` → `AuthAdminService.{TestOIDC,TestLDAP}` — admin.auth_config ✅ (OIDC = fetch .well-known ; LDAP = dial TCP ; échec = success:false)

## Étape 12 — Webhooks & API keys (par utilisateur)

- [x] `GET  /webhooks/options` → `WebhookService.GetOptions` — webhook.manage ✅
- [x] `GET/POST/PUT/DELETE /webhooks[...]` · `POST /webhooks/:id/test` → `WebhookService.{ListWebhooks,CreateWebhook,UpdateWebhook,DeleteWebhook,TestWebhook}` — webhook.manage ✅ (REST retiré ; secret/auth_header tri-state chiffrés)
- [x] `GET/POST/DELETE /api-keys[...]` → `APIKeyService.{ListApiKeys,CreateApiKey,DeleteApiKey}` — apikey.manage ✅ (REST retiré ; plaintext une seule fois)

---

## Points d'attention transversaux

- **Cookie JWT / login / logout** : **restent en REST** (décision Jonathan). Le mode connect-go `simple` n'expose pas `connect.Response`, donc poser un `Set-Cookie` en gRPC imposerait un interceptor+holder ; peu fréquent → pas rentable. Le cookie httpOnly posé par le REST est lu par les interceptors gRPC (`ConnectOptionalAuth`) sans souci.
- **Rate limiting login** : reste le middleware Echo par IP sur la route REST /auth/login.
- **OIDC login/callback** : redirections navigateur → **restent en REST** (pas migrables).
- **WebSockets** (`/events/ws`, `/exports/:id/ws`) → `server-stream`. connect-web gère le streaming côté front.
- **Binaire** (download/waveform/export/upload/logo) : **REST-only figé (15/07)** — base64 JSON (+33 %) + buffer mémoire complet, aucun gain du contrat typé sur des octets opaques. Réévaluer seulement si streaming progressif concret (→ server-stream Connect).
- **Upload : REST-only figé (15/07)** — en plus du binaire, **connect-web ne supporte PAS le client-streaming/bidi** (HTTP/1.1 = unary + server-stream uniquement), donc un upload streaming serait inutilisable côté navigateur. Multipart REST = streaming natif + progress. Un client-stream gRPC n'aurait de sens que pour un client NON-navigateur (device/script via clé API) — 2 chemins pour un gain marginal, non retenu.
- **Permissions** : reproduire `RequirePermission` en interceptor Connect (lire le rôle du contexte comme `ConnectOptionalAuth`, mais **bloquant**).
- **Swagger** : le `.proto` devient le contrat. Retirer swagger au fur et à mesure.
- **Frontend** : chaque service → un client dans `grpc.ts`, `api.ts` bascule fonction par fonction (mapping camelCase→app si besoin).
