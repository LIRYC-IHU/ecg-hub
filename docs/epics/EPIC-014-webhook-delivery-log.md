# EPIC-014 — Webhook Delivery Log & Renvoi Manuel

**Branch:** `feature/webhook-delivery-log`
**Date:** 2026-08-13
**Status:** To Do
**Depends on:** EPIC-010 (gRPC modules — WebhookService existant)

---

## Contexte

Le `Dispatcher` (`backend/internal/webhook/dispatcher.go`) livre déjà les événements aux webhooks utilisateur avec retry (3 tentatives, backoff), mais ne conserve que le **dernier** résultat sur la ligne `user_webhooks` (`last_status_code`, `last_error`, `last_delivered_at` — `RecordDelivery`, `user_webhook_repo.go:80`). Aucun historique des envois n'est gardé, et rien ne permet de renvoyer manuellement une notification échouée (le payload n'est jamais persisté).

Objectif : journaliser chaque envoi (payload, statut, erreur, date) en DB et exposer un endpoint de renvoi manuel depuis l'UI.

---

## Décisions d'architecture

| Question | Décision |
|----------|----------|
| Stockage | Nouvelle table `webhook_deliveries` (id, webhook_id FK CASCADE, event, payload jsonb, status_code, error, attempts, delivered_at, created_at) |
| Granularité du log | Un log = un événement dispatché (résultat final après retries), pas une ligne par tentative — évite le bruit, `attempts` indique combien de tentatives ont eu lieu |
| Rétention | Pas de purge automatique dans cette épique (todo si volume devient un problème) |
| Renvoi manuel | Rejoue le payload **stocké tel quel** (pas régénéré) via `Dispatcher.Deliver` — garantit que l'utilisateur revoit exactement ce qui a été envoyé |
| Retry sur renvoi manuel | Un seul essai, synchrone (pas de backoff) — l'utilisateur voit le résultat immédiatement dans l'UI |
| Permission | Réutilise `webhook.manage` (même permission que la gestion des webhooks) |
| Scope | Un utilisateur ne voit/renvoie que les livraisons de ses propres webhooks (même filtre que `ListWebhooks`) |

---

## Phase 1 — Persistance des livraisons (3h)

### Story 14.1 — Modèle & migration `webhook_deliveries`

**As a** système,
**I want** stocker chaque livraison de webhook (payload, statut, erreur),
**So that** l'historique survit et peut être consulté/rejoué plus tard.

**Acceptance Criteria :**
- [ ] Modèle `WebhookDelivery` dans `internal/db/models/` :
  ```go
  type WebhookDelivery struct {
      ID         string
      WebhookID  string // FK CASCADE -> user_webhooks.id
      Event      string
      Payload    datatypes.JSON // Payload complet envoyé (JSON du webhook.Payload)
      StatusCode int
      Error      string
      Attempts   int
      DeliveredAt time.Time
      CreatedAt   time.Time
  }
  ```
- [ ] Migration auto (AutoMigrate), index sur `webhook_id`
- [ ] FK `ON DELETE CASCADE` (supprimer un webhook supprime son historique)

**Estimation :** 1h

---

### Story 14.2 — Repo `WebhookDeliveryRepository`

**As a** système,
**I want** un repo dédié pour créer/lister les livraisons,
**So that** le dispatcher et l'API partagent le même accès données.

**Acceptance Criteria :**
- [ ] `Create(delivery)` — insère une ligne
- [ ] `ListByWebhook(webhookID, limit, offset)` — paginé, tri `delivered_at DESC`
- [ ] `Get(id)` — récupère une livraison (pour le renvoi manuel)

**Estimation :** 30min

---

### Story 14.3 — Brancher le Dispatcher

**As a** système,
**I want** que chaque `deliverWithRetry` écrive une ligne `webhook_deliveries` en plus de `RecordDelivery`,
**So that** l'historique se remplit automatiquement sans changer le comportement de livraison existant.

**Acceptance Criteria :**
- [ ] `deliverWithRetry` (`dispatcher.go:261`) appelle `deliveryRepo.Create` après la boucle de retry, avec le payload JSON, le statut final, l'erreur finale, le nombre de tentatives
- [ ] Échec de l'écriture du log ne bloque jamais la livraison (best-effort, `slog.Warn`)
- [ ] Tests existants (`dispatcher_test.go`) toujours verts + un test couvrant l'insertion du log

**Estimation :** 1h30

---

## Phase 2 — API de consultation & renvoi (3h)

### Story 14.4 — Endpoint liste des livraisons

**As a** utilisateur avec `webhook.manage`,
**I want** lister l'historique des envois d'un de mes webhooks,
**So that** je peux diagnostiquer les échecs.

**Acceptance Criteria :**
- [ ] Méthode gRPC/Connect `ListDeliveries(webhook_id, page)` sur `WebhookService`
- [ ] Vérifie que le webhook appartient à l'appelant (même contrôle que `ListWebhooks`/`GetWebhook`)
- [ ] Retourne `{ event, status_code, error, attempts, delivered_at }` (payload inclus ou en détail à la demande — à trancher en implémentation, éviter de gonfler la liste)
- [ ] Paginé (limite par défaut raisonnable, ex. 50)

**Estimation :** 1h30

---

### Story 14.5 — Endpoint renvoi manuel

**As a** utilisateur avec `webhook.manage`,
**I want** renvoyer manuellement une livraison échouée,
**So that** je n'ai pas besoin d'attendre un nouvel événement pour retester mon receiver.

**Acceptance Criteria :**
- [ ] Méthode `ResendDelivery(delivery_id)` sur `WebhookService`
- [ ] Charge la livraison + le webhook associé, vérifie l'appartenance à l'appelant
- [ ] Rejoue le payload stocké via `Dispatcher.Deliver` (un seul essai, synchrone)
- [ ] Écrit une **nouvelle** ligne `webhook_deliveries` pour le renvoi (ne modifie pas l'ancienne — traçabilité)
- [ ] Retourne le résultat (statut/erreur) directement dans la réponse pour feedback immédiat
- [ ] Audit log : `webhook_delivery_resent`

**Estimation :** 1h30

---

## Phase 3 — UI (3h)

### Story 14.6 — Historique des livraisons dans WebhooksPage

**As a** utilisateur,
**I want** voir l'historique des envois d'un webhook et pouvoir en renvoyer un,
**So that** je peux diagnostiquer et corriger sans attendre un nouvel événement réel.

**Acceptance Criteria :**
- [ ] Dans `frontend/src/components/settings/WebhooksPage.tsx`, panneau/accordéon "Historique" par webhook
- [ ] Liste : événement, date, statut (badge vert/rouge), erreur si échec
- [ ] Bouton "Renvoyer" par ligne → appelle `ResendDelivery`, affiche le nouveau résultat (toast ou mise à jour de la liste)
- [ ] Pagination simple (bouton "charger plus")

**Estimation :** 3h

---

## Résumé

| Phase | Stories | Estimation |
|-------|---------|------------|
| 1 — Persistance | 14.1 → 14.3 | 3h |
| 2 — API | 14.4 → 14.5 | 3h |
| 3 — UI | 14.6 | 3h |

**Total estimé :** ~9h

---

## Hors scope (todo futur)

- Purge/rétention automatique de l'historique (volume à surveiller)
- Log par tentative individuelle (actuellement : un log = résultat final après retries)
- Renvoi en masse (plusieurs livraisons à la fois)
