# EPIC-011 — Security Hardening : Production Readiness

**Branch:** `feature/security-hardening`
**Date:** 2026-05-31
**Status:** ✅ Implémentée le 2026-05-31 (Phases 1, 2 et 3) — validation Docker build + déploiement TLS à faire
**Depends on:** Core auth (EPIC-006), Input validation (EPIC-008)
**Priorité globale:** 🔴 CRITIQUE — bloquant avant tout déploiement production (données de santé / HDS-RGPD)

---

## Contexte

L'audit complet du projet (backend Go + frontend React) a identifié plusieurs failles de sécurité importantes pour un hub manipulant des données de santé identifiantes (ECG patients). Cette EPIC regroupe l'ensemble des correctifs sécurité à appliquer avant mise en production.

Le projet a déjà de bonnes fondations (requêtes paramétrées, token en cookie HttpOnly, bcrypt cost 12, AES-256-GCM, OIDC state signé HMAC, audit append-only). Les manques concernent principalement : le chiffrement de transport (TLS), la robustesse de l'authentification (anti-bruteforce), le durcissement du déploiement, et la gestion sécurisée de la clé de chiffrement.

---

## Décisions d'architecture

| Question | Décision |
|----------|----------|
| Terminaison TLS | nginx (443 + HSTS) en priorité ; option `e.StartTLS` pour bare-metal |
| Détection mode prod | Variable `APP_ENV=production` ou `server.tls: true` → active les gardes fail-fast |
| Rate-limiting | `middleware.RateLimiter` Echo (in-memory store) + compteur d'échecs par compte |
| Clé de chiffrement | Fail-fast si absente ou égale au défaut en mode prod |
| Cookie | `Secure: true` conditionné au mode TLS (dev reste fonctionnel en HTTP) |

---

## Phase 1 — Correctifs P0 (bloquants)

### Story 11.1 — Fail-fast sur AUTH_ENCRYPTION_KEY

**As a** ops responsable de la sécurité,
**I want** que le serveur refuse de démarrer en production sans clé de chiffrement valide,
**So that** les secrets stockés en DB (OIDC/LDAP/FTP/HL7/connecteurs) ne soient jamais chiffrés avec la clé par défaut publique.

**Contexte technique :** `main.go:399-403` — actuellement, si `AUTH_ENCRYPTION_KEY` est absent, fallback silencieux sur `"ecg-hub-dev-key-do-not-use-in-prod"` (simple `slog.Warn`). Cette clé est publique → le chiffrement AES-GCM de tous les secrets DB est inopérant.

**Acceptance Criteria :**
- [ ] En mode production (`APP_ENV=production` ou `server.tls: true`) : `os.Exit(1)` si `AUTH_ENCRYPTION_KEY` est vide ou égal au défaut
- [ ] En mode dev : conserver le warning + fallback (rétrocompat)
- [ ] Valider une longueur minimale (≥ 32 caractères) en mode prod
- [ ] Ajouter `AUTH_ENCRYPTION_KEY` à `.env.example` avec commentaire explicatif
- [ ] Documenter la procédure de rotation de clé (re-chiffrement des secrets existants)

**Estimation :** 2h

---

### Story 11.2 — Activation TLS / HTTPS

**As a** RSSI,
**I want** que tout le trafic transite en HTTPS,
**So that** les données ECG (données de santé) ne soient jamais exposées en clair sur le réseau (conformité HDS/RGPD).

**Contexte technique :** `main.go:597` démarre en HTTP en clair (`port := ":4444"`), ignorant `cfg.Server.Port` et `cfg.Server.TLS`. `nginx.conf:2` écoute uniquement sur `:80`.

**Acceptance Criteria :**
- [ ] nginx : ajouter `listen 443 ssl`, configuration des certificats, et redirection 301 HTTP→HTTPS
- [ ] nginx : en-tête `Strict-Transport-Security: max-age=31536000; includeSubDomains`
- [ ] Backend : utiliser `cfg.Server.Port` au lieu du port `:4444` codé en dur
- [ ] Backend : si `cfg.Server.TLS: true`, utiliser `e.StartTLS` avec cert/key (option bare-metal)
- [ ] Documenter le déploiement TLS (certificats Let's Encrypt ou internes) dans le README
- [ ] docker-compose : exposer le port 443

**Estimation :** 4h

---

### Story 11.3 — Flag Secure sur le cookie JWT

**As a** utilisateur,
**I want** que mon cookie de session ne soit jamais transmis en clair,
**So that** mon token ne puisse pas être intercepté lors d'un downgrade HTTP.

**Contexte technique :** `auth.go:22-31` (`setJWTCookie`) et `:34-43` (`clearJWTCookie`) — `HttpOnly` ✅ et `SameSite=Lax` ✅ présents, mais `Secure` absent.

**Acceptance Criteria :**
- [ ] Ajouter `Secure: true` sur `setJWTCookie` et `clearJWTCookie`
- [ ] Conditionner au mode TLS (helper `isSecureContext(c)` ou config) pour préserver le dev en HTTP
- [ ] Vérifier que le flow OIDC callback pose aussi le cookie avec les bons flags
- [ ] Test : le cookie posté en prod a bien `Secure; HttpOnly; SameSite=Lax`

**Estimation :** 1h

---

## Phase 2 — Correctifs P1 (élevés)

### Story 11.4 — Rate-limiting & anti-bruteforce

**As a** système,
**I want** limiter le nombre de tentatives de connexion par IP et par compte,
**So that** les endpoints d'authentification résistent aux attaques par force brute.

**Contexte technique :** `auth.go:99` (`LoginHandlerWithDB`) — aucune limitation. Aucun middleware rate-limit dans tout le backend.

**Acceptance Criteria :**
- [ ] `middleware.RateLimiter` Echo appliqué globalement (ex. 100 req/min/IP) ou ciblé `/api/v1/auth/*`
- [ ] Rate-limit strict sur `/auth/login` (ex. 5 tentatives / 5 min / IP)
- [ ] Compteur d'échecs par compte avec lockout temporaire (ex. 10 échecs → blocage 15 min)
- [ ] Réinitialisation du compteur sur login réussi
- [ ] Audit log des dépassements (action `login_rate_limited`, `account_locked`)
- [ ] Headers `Retry-After` sur réponse 429

**Estimation :** 4h

---

### Story 11.5 — Durcissement du conteneur Docker

**As a** ops,
**I want** que le backend s'exécute avec un utilisateur non-privilégié,
**So that** une compromission du conteneur n'offre pas un accès root.

**Contexte technique :** `backend/Dockerfile` — aucune directive `USER`, le binaire tourne en root.

**Acceptance Criteria :**
- [ ] Dockerfile : créer un utilisateur non-root (`adduser -D -u 10001 app`) et `USER app`
- [ ] Vérifier les permissions des volumes `/data/ecg` et `/data/ecg-quarantine` pour cet utilisateur
- [ ] docker-compose : `read_only: true` (avec tmpfs pour /tmp), `cap_drop: [ALL]`, `security_opt: [no-new-privileges:true]`
- [ ] Le conteneur démarre et fonctionne correctement en non-root
- [ ] Scan de vulnérabilités image (Trivy) intégré au build (optionnel)

**Estimation :** 3h

---

### Story 11.6 — En-têtes de sécurité HTTP & BodyLimit

**As a** RSSI,
**I want** que l'application pose les en-têtes de sécurité standards et borne la taille des requêtes,
**So that** on réduise la surface d'attaque (clickjacking, MIME-sniffing, DoS mémoire).

**Contexte technique :** ni Echo (`main.go`) ni nginx ne posent CSP/X-Frame-Options/X-Content-Type-Options/Referrer-Policy. Aucun `BodyLimit`.

**Acceptance Criteria :**
- [ ] `e.Use(middleware.Secure())` avec CSP adaptée au frontend (React + assets Vite)
- [ ] En-têtes : `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`
- [ ] `e.Use(middleware.BodyLimit("10M"))` (ajustable, couvre upload logo/branding)
- [ ] Limite spécifique cohérente sur l'upload de logo (`UploadLogoHandler`)
- [ ] Vérifier que la CSP ne casse pas le viewer ECG ni Swagger UI

**Estimation :** 3h

---

## Phase 3 — Correctifs P2 (moyens)

### Story 11.7 — Anti-énumération d'utilisateurs

**As a** système,
**I want** que le temps de réponse du login soit constant que l'utilisateur existe ou non,
**So that** un attaquant ne puisse pas énumérer les comptes valides par analyse de timing.

**Contexte technique :** `local.go:37-45` — retour immédiat si l'utilisateur n'existe pas ; sinon bcrypt (~250ms). Différence mesurable.

**Acceptance Criteria :**
- [ ] Si l'utilisateur n'existe pas, comparer le mot de passe contre un hash bcrypt factice pré-calculé (même coût)
- [ ] Message d'erreur identique dans tous les cas (`invalid credentials`)
- [ ] Test : écart de timing < seuil mesurable entre utilisateur existant/inexistant

**Estimation :** 1h

---

### Story 11.8 — Suppression des fuites d'information dans les erreurs

**As a** RSSI,
**I want** que les réponses API n'exposent pas de détails techniques internes,
**So that** un attaquant n'obtienne pas d'information sur l'implémentation.

**Contexte technique :** plusieurs handlers renvoient `err.Error()` brut au client (ex. `ecg_waveform.go:87` "Failed to convert... : "+err.Error()`).

**Acceptance Criteria :**
- [ ] Auditer tous les handlers renvoyant `err.Error()` au client
- [ ] Messages génériques côté client + log détaillé côté serveur (slog avec request ID)
- [ ] Conserver les messages métier utiles (validation), masquer les erreurs système/conversion/SQL
- [ ] Vérifier les erreurs OIDC/LDAP (pas de fuite d'URL interne ou de config)

**Estimation :** 2h

---

## Résumé

| Story | Priorité | Estimation | Dépendance | Statut |
|-------|----------|------------|------------|--------|
| 11.1 — Fail-fast clé chiffrement | 🔴 P0 | 2h | — | ✅ Done |
| 11.2 — Activation TLS/HTTPS | 🔴 P0 | 4h | — | ✅ Done |
| 11.3 — Cookie Secure | 🔴 P0 | 1h | 11.2 | ✅ Done |
| 11.4 — Rate-limiting & anti-bruteforce | 🟠 P1 | 4h | — | ✅ Done |
| 11.5 — Durcissement Docker non-root | 🟠 P1 | 3h | — | ✅ Done |
| 11.6 — En-têtes sécurité & BodyLimit | 🟠 P1 | 3h | — | ✅ Done |
| 11.7 — Anti-énumération login | 🟡 P2 | 1h | — | ✅ Done |
| 11.8 — Fuites d'info erreurs | 🟡 P2 | 2h | — | ✅ Done (surfaces non-admin/publiques ; admin conservé) |

**Total estimé :** ~20h
**Bloquant production :** Stories 11.1, 11.2, 11.3 (Phase 1)
