# Dossier d'architecture technique — ECG Hub

| | |
|---|---|
| **Application** | ECG Hub |
| **Version décrite** | 1.1.0 |
| **Date** | 5 octobre 2026 |
| **Éditeur** | IHU LIRYC |
| **Dépôt** | https://github.com/LIRYC-IHU/ecg-hub |
| **Licence** | voir `LICENSE` |

Ce document décrit l'architecture technique, les flux et les conditions
d'exploitation d'ECG Hub en vue d'une mise en production. Il complète la
documentation de déploiement du dépôt, à laquelle il renvoie plutôt que de la
recopier :

- [`docs/deploy-prod.md`](deploy-prod.md) — déploiement, modes réseau, ports
- [`docs/TLS_CERTS.md`](TLS_CERTS.md) — certificats FTPS et DICOM TLS
- [`docs/POSTGRES_UPGRADE.md`](POSTGRES_UPGRADE.md) — montées de version base
- [`docs/HL7_INBOUND_ADT.md`](HL7_INBOUND_ADT.md) — HL7 ADT entrant (IHE RAD-12)
- [`SECURITY.md`](../SECURITY.md) — signalement de vulnérabilité

Les sections marquées **[À COMPLÉTER PAR L'ÉTABLISSEMENT]** portent sur des
décisions ou des moyens qui n'appartiennent pas à l'éditeur : adressage,
hébergement, politique de sauvegarde, durées de conservation. Elles sont
laissées explicites plutôt que remplies par des valeurs plausibles.

---

## 1. Objet et périmètre

### 1.1 Fonction

ECG Hub centralise les électrocardiogrammes produits par les appareils d'un
établissement, quel que soit leur constructeur, et les met à disposition sous
une forme unique : consultation web, export, transmission vers un PACS ou un
DPI.

L'application **termine elle-même les protocoles des appareils** — c'est le
point qui structure toute son architecture. Aucun agent n'est installé sur les
électrocardiographes ; ce sont eux qui poussent vers ECG Hub, avec leurs
protocoles d'origine.

### 1.2 Chaîne de traitement

1. **Réception** — l'appareil dépose un fichier en FTP/FTPS, ouvre une
   association DICOM C-STORE, ou émet en ECTP (Nihon Kohden).
2. **Reconnaissance et analyse** — un module constructeur identifie le format,
   le valide et le convertit vers un modèle commun. Un fichier illisible part en
   quarantaine ; un fichier lisible mais sans identifiant patient exploitable
   entre dans la file « non identifiés ».
3. **Enrichissement de l'identité** — l'identifiant patient porté par l'ECG sert
   de clé à une requête HL7 `QRY^A19` vers le SIH, qui renvoie les données
   démographiques. Le SIH peut aussi pousser des mises à jour (`ADT^A08`, `A40`).
4. **Mise à disposition** — consultation dans l'interface web, export ZIP,
   webhooks, recopie vers des systèmes externes (PACS en DICOM C-STORE, points
   ECTP/FTP), et exposition IHE pour un DPI.

### 1.3 Hors périmètre

- Aucune fonction diagnostique. L'application n'interprète pas les tracés et
  n'émet aucune mesure automatique. L'interface porte une mention explicite en
  ce sens.
- Aucune notion d'établissement ou de site : une instance sert un seul domaine
  d'identification patient (voir §4).
- L'application ne gère pas ses propres certificats web ; la terminaison TLS du
  front est assurée en amont.

### 1.4 Formats pris en charge

Philips, GE MUSE, Nihon Kohden (`.DAT`, ECTP), Mindray, Fukuda, FDA aECG XML,
DICOM waveform. Les conversions (PDF, DICOM, aECG) s'appuient sur l'outil
`ecg-bridge` v1.4.0, embarqué dans l'image.

---

## 2. Architecture applicative

### 2.1 Composants

| Composant | Technologie | Rôle |
|---|---|---|
| `backend` | Go (Echo, Connect/gRPC, GORM) | API, serveurs de protocoles appareils, pipeline d'ingestion, ordonnanceur HL7 |
| `frontend` | React 19 + nginx | SPA de consultation et d'administration, et proxy inverse vers l'API |
| `db` | PostgreSQL 18 | Métadonnées, identités patient, configuration, traçabilité |
| Volume `ecg` | Système de fichiers ou S3 | Fichiers ECG d'origine |
| Volume `ecg-quarantine` | Système de fichiers | Fichiers rejetés à l'analyse |

Le backend est un **processus unique**. Les serveurs FTP, DICOM, ECTP et HL7, le
pipeline d'ingestion et l'API cohabitent dans ce processus et communiquent par
canaux internes. Cette conception a une conséquence d'exploitation directe,
traitée au §3.3 : l'application ne se déploie pas en plusieurs instances
actives.

### 2.2 Modularité

Les modules constructeurs démarrent et s'arrêtent depuis l'interface
d'administration, sans redémarrage du serveur. Un établissement n'active que les
modules correspondant à son parc.

### 2.3 Configuration

Deux niveaux, volontairement séparés :

- **`config.yaml`** — infrastructure seule : ports, chemins de volumes, pool de
  connexions, plafonds. Monté en lecture seule.
- **Base de données, via l'interface d'administration** — tout le
  fonctionnel : modules et leurs ports, paramètres HL7 et mappings de champs,
  connecteurs de recopie, webhooks, fournisseurs d'authentification, rôles.

Un changement fonctionnel ne demande donc ni modification de fichier ni
redémarrage, et reste traçable dans le journal d'audit.

Les secrets ne sont jamais dans `config.yaml` : ils arrivent par variables
d'environnement (voir annexe A), ce qui permet l'usage d'un coffre.

---

## 3. Architecture de déploiement

### 3.1 Vue d'ensemble

```
                    Praticiens                    Appareils (VLAN médical)
                        │                                   │
                        │ HTTPS 443                         │ FTP 21 + 30000-30100
                        ▼                                   │ DICOM 4242
              ┌──────────────────┐                          │ ECTP 30003
              │  Reverse proxy   │  TLS terminé ici         │
              │  (établissement) │                          │
              └────────┬─────────┘                          │
                       │ HTTP 80                            │
    ┌──────────────────┼────────────────────────────────────┼──────────────┐
    │  Hôte ECG Hub    ▼                                    ▼              │
    │           ┌─────────────┐                    ┌─────────────────┐     │
    │           │  frontend   │                    │                 │     │
    │           │   nginx     │───── 4444 ────────▶│     backend     │     │
    │           │     SPA     │                    │                 │     │
    │           └─────────────┘                    └───┬────┬────┬───┘     │
    │                                                  │    │    │         │
    │                             ┌────────────────────┘    │    │         │
    │                             ▼                         ▼    ▼         │
    │                      ┌─────────────┐        ┌──────┐ ┌──────────┐    │
    │                      │ PostgreSQL  │        │ ecg  │ │quarantine│    │
    │                      │     18      │        │volume│ │  volume  │    │
    │                      └─────────────┘        └──────┘ └──────────┘    │
    └───────────────────────────┬──────────────────────┬───────────────────┘
                                │ HL7 MLLP             │ DICOM C-STORE
                                │ 2575 sortant         │ sortant
                                │ 2576 entrant         │ IHE 8443 entrant
                                ▼                      ▼
                          SIH / DPI                  PACS
```

Ni le port HTTP du backend (4444) ni PostgreSQL ne sont publiés : seuls nginx et
le backend les atteignent.

### 3.2 Les deux modes réseau

Le choix est structurant et se fait **avant** la mise en service, car il
détermine une fonction de sécurité.

| | Mode *bridge* (défaut) | Mode *host* |
|---|---|---|
| Fichier | `docker-compose.yml` | `docker-compose.host.yml` |
| Isolation réseau | Oui, conteneurs sur réseau dédié | Non, l'application voit les interfaces de l'hôte |
| Ports | Publiés explicitement | Liaison directe |
| Adresse source des appareils | Perdue (traduite par Docker) | Préservée |
| **Identification par adresse MAC** | **Impossible** | Possible, si et seulement si les appareils sont sur le même segment de niveau 2 que l'hôte |
| Port 21 | Assuré par le démon Docker, l'application reste non privilégiée | Redirection 21 → 2121 sur l'hôte (`make ftp-ports`) |

L'identification par adresse MAC lit la table de voisinage du système. Or une
table ARP ne contient que des voisins du même segment de niveau 2 : aucune
requête ARP n'est émise pour une adresse qui n'est pas sur le réseau local.
**Dès que les appareils et le serveur sont sur des VLAN distincts — la topologie
hospitalière habituelle — aucun appareil ne peut être identifié**, et ce quel
que soit le mode réseau ou l'orchestrateur. Le trafic est routé, la seule
adresse matérielle visible est celle du routeur, et le code refuse délibérément
de la rendre : l'approuver approuverait tout ce qui se trouve derrière.

En mode *bridge*, l'obstacle survient plus tôt encore : le conteneur possède son
propre espace de noms réseau et n'y voit que les conteneurs voisins.

Le comportement face à un appareil non identifié est un réglage, et les deux
valeurs sont inconfortables. Laissé à sa valeur par défaut, le contrôle
journalise un avertissement et laisse passer — un contrôle qui n'interdit rien,
indiscernable à l'écran d'un contrôle qui fonctionne. Activé
(`deny_unidentified`), il refuse **tous** les appareils et interrompt
l'ingestion clinique. D'où la désactivation par défaut, qui est délibérée.

**Qualification.** Cette fonction est un **inventaire d'appareils** — nommer les
appareils qui parlent au hub, les enrôler, en révoquer un, compter leurs
contacts — et non une mesure de sécurité : une adresse MAC s'usurpe en une
commande, et elle n'est de toute façon pas vérifiable sur réseau routé. Elle ne
doit pas être comptée comme un contrôle d'accès dans une analyse de risque.

**Conséquence :** le contrôle d'accès à ces ports repose sur le cloisonnement
réseau (§6, §7), pas sur cette fonction. Si une exigence porte explicitement sur
un filtrage matériel, elle suppose les appareils et le serveur sur un même
segment de niveau 2, et doit être arbitrée comme telle (§Annexe B).

### 3.3 Instance unique

L'application se déploie en **une seule instance active**, pour trois raisons
indépendantes :

1. Les appareils sont configurés à la main avec une adresse unique ; il n'y a
   pas de découverte de service côté appareil.
2. Le pipeline d'ingestion est en mémoire dans le processus. Deux instances ne
   partagent pas d'état de traitement.
3. Une connexion de contrôle FTP et sa connexion de données doivent atteindre le
   même processus.

La montée en charge se fait verticalement. Le §5 montre que la marge est
confortable : la charge de calcul d'un ECG est de l'ordre de quelques dizaines
de millisecondes CPU.

### 3.4 Kubernetes

Non livré à ce jour. L'application web et la base ne poseraient pas de
difficulté, mais les services exposés aux appareils sont en couche 4 et
demandent une validation sur cluster réel :

- La plage de ports `NodePort` par défaut d'un cluster (30000-32767) exclut les
  ports 21 et 4242, or les appareils Nihon Kohden composent le 21 et ne sont pas
  paramétrables. Un `Service` de type `LoadBalancer` est donc la voie à retenir.
- L'adresse du `LoadBalancer` doit être réservée : un changement impliquerait de
  repasser sur chaque appareil du site.
- `externalTrafficPolicy: Local` est nécessaire pour conserver l'adresse source,
  seule identité du client enregistrée dans la traçabilité d'ingestion.
- Réplica unique, stratégie `Recreate` (§3.3).
- L'identification par adresse MAC suppose les nœuds raccordés au même segment
  de niveau 2 que les appareils. Monter le `/proc/net` de l'hôte en lecture
  seule suffit à la rendre techniquement accessible au *pod* — `hostNetwork`
  n'est pas requis — mais cela ne change rien sur un réseau routé, où aucune
  entrée ARP n'existe pour l'appareil (§3.2). Vérifié sur le cluster de test :
  nœuds en `10.10.50.0/24`, appareil en `10.10.30.0/24`, aucune identification
  possible.

Un diagramme de l'architecture Kubernetes envisagée existe
(`docs/diagrams/ecg-hub-kubernetes.html`). La production des manifestes demande
une phase de test dédiée sur les flux appareils.

---

## 4. Domaine d'identification patient — prérequis critique

> **À lire avant tout déploiement.**

ECG Hub rattache un ECG à un patient **sur le seul identifiant patient**. Rien
n'est apparié sur le nom, la date de naissance ou le sexe — délibérément, car
l'appariement démographique est le mécanisme par lequel un tracé finit dans le
dossier d'un autre patient.

Cette conception a un prérequis, qui porte sur l'installation et non sur le
logiciel :

> **L'identifiant patient doit être unique sur l'ensemble du périmètre servi par
> l'instance.**

La colonne `patients.patient_id` est globalement unique. Si deux sites peuvent
émettre le même numéro pour deux personnes différentes, le second ECG se
rattache au dossier de la première — silencieusement, sans divergence
détectable, puisqu'aucune donnée démographique n'est comparée. Il n'existe pas
de colonne d'autorité d'affectation pour les distinguer.

Avant tout déploiement couvrant plusieurs sites, établir lequel des deux cas
s'applique :

- **Domaine d'identification unique sur tous les sites** — déployer une
  instance. *L'AP-HP est dans ce cas : les identifiants patient sont uniques à
  l'échelle de l'institution, non par site.*
- **Domaines par site** — déployer une instance par domaine, ou préfixer
  l'identifiant à l'ingestion pour rendre la valeur combinée unique. Ne jamais
  faire pointer deux domaines vers une instance.

Les identifiants scannés sur un bracelet (NIP, MRN) sont pris en charge comme
clé de requête : le SIH peut répondre avec un identifiant différent de celui
demandé, et le patient est alors déplacé vers l'identifiant de
l'établissement, ECG compris.

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]** Cas applicable et confirmation écrite de
l'unicité de l'identifiant sur le périmètre.

---

## 5. Dimensionnement

### 5.1 Mesure de référence

Le dépôt fournit `backend/loadtest/capacity-probe.sh`, qui mesure le coût réel
d'un ECG sur l'installation plutôt que de l'estimer. Il ingère un volume donné
en relevant les métriques Prometheus avant et après, puis extrapole.

Mesure sur un déploiement à 2 cœurs : **environ 27 ms CPU par ECG**.

### 5.2 Extrapolation

Sur cette base, pour une charge de 5 000 ECG par jour :

| Grandeur | Valeur |
|---|---|
| Coût CPU cumulé | ≈ 2,3 cœur-minutes par jour |
| Charge moyenne | < 0,01 cœur |
| Charge en pointe (facteur 3 sur 8 h) | < 0,05 cœur |

Le calcul n'est pas le facteur limitant. Les dimensions réelles sont le **débit
réseau**, le **stockage** et les **entrées/sorties**, l'ingestion étant par
nature en rafales.

### 5.3 Ressources recommandées

Valeurs de départ, à ajuster après une campagne `capacity-probe.sh` sur
l'installation :

| Ressource | Recommandation | Remarque |
|---|---|---|
| vCPU | 4 | 2 suffisent au calcul ; la marge absorbe les rafales et les conversions PDF/DICOM |
| RAM | 8 Go | dont PostgreSQL ; chaque étape du pipeline tient le fichier en mémoire |
| Disque système | 40 Go | OS, images, journaux |
| Volume ECG | voir §5.4 | dimensionné sur la conservation |
| Volume quarantaine | 5 à 10 % du volume ECG | rétention propre possible |
| Volume base | 20 Go pour 1 million d'ECG | métadonnées seules, pas de tracé |

### 5.4 Volumétrie de stockage

Les fichiers sont conservés **non compressés**. Ordres de grandeur par ECG 12
dérivations au repos, selon le format :

| Format | Taille unitaire indicative |
|---|---|
| Nihon Kohden `.DAT`, Fukuda `.ECG` | 10 à 30 Ko |
| XML (Philips, GE MUSE, aECG) | 100 à 500 Ko |
| DICOM waveform | 100 à 300 Ko |

À 5 000 ECG par jour en XML à 300 Ko, soit environ 1,5 Go par jour : **≈ 550 Go
par an**.

La compression au niveau du système de fichiers ou du stockage est **vivement
recommandée** : les formats XML descendent autour du quart de leur taille.

Le plafond `storage.max_size` (`config.yaml`) est une limite souple sur le
volume principal. La rotation ne supprime de fichiers cliniques que si elle est
explicitement activée, et jamais en mode S3.

### 5.5 Plafond par fichier

`ingest.max_file_bytes` limite un fichier entrant à chaque frontière du
pipeline, **1 Mio par défaut**. Chaque étape tient le fichier en mémoire et les
ports FTP et DICOM sont exposés aux appareils : un envoi non borné suffisait à
pousser le backend vers le tueur de processus par épuisement mémoire. Relever
cette valeur si le parc produit des fichiers plus volumineux — et la relever
pour cette raison seulement.

### 5.6 Mode S3

En mode S3, le volume local reste utilisé comme file d'attente : les fichiers
sont écrits sur disque puis téléversés par une tâche de fond. L'ingestion
n'attend jamais le réseau, et une indisponibilité du *bucket* coûte un retard,
pas un ECG perdu. **Dimensionner le volume local sur la plus longue
indisponibilité que l'on accepte d'absorber.**

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]** Volume d'ECG par jour attendu, parc
d'appareils et formats, durée de conservation retenue (§8).

---

## 6. Matrice de flux

### 6.1 Flux entrants

| Source | Destination | Port | Protocole | Chiffré | Objet |
|---|---|---|---|---|---|
| Postes praticiens | Reverse proxy | 443 | HTTPS | Oui | Interface web et API |
| Reverse proxy | Hôte ECG Hub | 80 | HTTP | Non — flux interne | SPA et API |
| Électrocardiographes | Hôte ECG Hub | 21 | FTP / FTPS (`AUTH TLS`) | Optionnel, voir §7.2 | Dépôt de fichiers, contrôle |
| Électrocardiographes | Hôte ECG Hub | 30000-30100 | FTP mode passif | idem contrôle | Données de transfert |
| Appareils / PACS | Hôte ECG Hub | 4242 | DICOM C-STORE SCP | Optionnel (DICOM TLS) | Réception DICOM |
| Appareils Nihon Kohden | Hôte ECG Hub | 30003 | ECTP | Non (protocole constructeur) | Réception NK |
| SIH | Hôte ECG Hub | 2576 | HL7 v2 MLLP | Non — à encadrer par le réseau | ADT entrant (IHE RAD-12), désactivé par défaut |
| DPI | Hôte ECG Hub | 8443 | HTTPS + mTLS | Oui, mutuellement authentifié | IHE CARD-5, CARD-6, ITI-11, désactivé par défaut |
| Prometheus | Hôte ECG Hub | 9091 | HTTP | Non — lié à la boucle locale | Collecte de métriques |

### 6.2 Flux sortants

| Source | Destination | Port | Protocole | Chiffré | Objet |
|---|---|---|---|---|---|
| ECG Hub | SIH | 2575 (configurable) | HL7 v2 MLLP | Non — à encadrer par le réseau | Requête démographique `QRY^A19`, résultat `ORU` |
| ECG Hub | PACS | 104 / 11112 (configurable) | DICOM C-STORE SCU | Optionnel (DICOM TLS) | Recopie vers PACS |
| ECG Hub | Points ECTP / FTP | configurable | ECTP / FTP | selon la cible | Recopie vers systèmes tiers |
| ECG Hub | Points HTTP | 443 | HTTPS | Oui | Webhooks |
| ECG Hub | Base de données | 5432 | PostgreSQL | `sslmode` configurable | Métadonnées |
| ECG Hub | Stockage objet | 443 | HTTPS (S3) | Oui | Stockage des fichiers, mode S3 |
| ECG Hub | Annuaire | 389 / 636 | LDAP / LDAPS | Recommandé : LDAPS | Authentification |
| ECG Hub | Fournisseur OIDC | 443 | HTTPS | Oui | Authentification |

### 6.3 Observations

Trois éléments doivent concorder, faute de quoi un port ne mène nulle part
silencieusement : le port côté conteneur, la valeur dans
**Administration > Modules**, et la règle de pare-feu en amont.

Le mode passif FTP mérite une attention particulière. En FTPS, le suivi de
connexion du pare-feu est aveugle au canal de contrôle chiffré : il ne peut pas
déduire les ports de données. **La plage 30000-30100 doit donc être ouverte
explicitement**, sur le pare-feu comme sur l'hôte. La largeur de la plage est le
plafond de transferts simultanés : un port est retenu par connexion de données
en cours.

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]** Adresses et VLAN de chaque extrémité,
numéros de dossier de demande d'ouverture de flux.

---

## 7. Sécurité

### 7.1 Authentification et habilitations

**Modes d'authentification** — local (identifiant et mot de passe), OIDC, LDAP.
Cumulables ; configurés depuis l'interface d'administration.

L'identité interne est unifiée : chaque utilisateur possède un identifiant
propre à l'application, indépendant du *sub* OIDC ou du DN LDAP, de sorte qu'un
changement côté annuaire ne dissocie pas un compte de son historique. L'identité
LDAP repose sur un attribut stable configurable (`uuid_attribute`), non sur le
nom d'utilisateur.

**Habilitations** — rôles portant une vingtaine de permissions unitaires,
vérifiées sur chaque procédure de l'API :

```
ecg.read ecg.write ecg.upload ecg.download ecg.delete ecg.send_result
patient.read
quarantine.read quarantine.assign quarantine.delete
device.read device.manage
tag.create tag.apply tag.delete
webhook.manage apikey.manage
admin.users admin.roles admin.audit admin.system admin.auth_config admin.branding
```

Une procédure dépourvue de permission associée est **refusée**, et un test
automatisé détecte l'omission avant la mise en production. Une anomalie
antérieure, où une telle procédure était servie à tout appelant authentifié, a
été corrigée en 1.1.0.

**Clés d'API** — pour les intégrations techniques, portant les mêmes
permissions, révocables.

### 7.2 Chiffrement des flux

| Flux | Mécanisme |
|---|---|
| Interface web | HTTPS, terminé sur le proxy de l'établissement |
| FTP | FTPS explicite (`AUTH TLS`). Quand TLS est actif, **le chiffrement est obligatoire** : une session en clair est refusée |
| DICOM | DICOM TLS, optionnel |
| IHE (CARD-5/6, ITI-11) | HTTPS avec authentification mutuelle obligatoire |
| HL7 MLLP | Pas de TLS dans le protocole — à encadrer par le cloisonnement réseau |
| Base de données, S3, LDAP, OIDC | TLS selon configuration ; LDAPS et `sslmode` recommandés |

Activer TLS sur un module sans certificat exploitable est **refusé au démarrage
avec le motif énoncé**, plutôt qu'accepté puis mis en échec sur chaque client.

Les certificats des serveurs appareils sont lus depuis un répertoire monté,
jamais stockés en base : le renouvellement reste chez qui le pratique déjà
(certbot, PKI interne, certificat générique d'établissement). Détails dans
[`docs/TLS_CERTS.md`](TLS_CERTS.md).

### 7.3 Chiffrement au repos

Les identifiants de connexion des fournisseurs d'authentification et des modules
sont chiffrés en **AES-256-GCM** avec la clé `AUTH_ENCRYPTION_KEY`.

> **Avertissement.** La perte de `AUTH_ENCRYPTION_KEY` rend illisible tout
> secret déjà stocké en base. Cette clé doit être sauvegardée **avec** la base,
> et conservée **ailleurs** qu'à côté d'elle.

Le chiffrement du stockage des fichiers ECG et de la base relève de
l'infrastructure (chiffrement de volume, de système de fichiers, ou du stockage
objet) et n'est pas assuré par l'application.

### 7.4 Durcissement des conteneurs

Le backend s'exécute sous un utilisateur non privilégié (uid 10001), avec
`cap_drop: ALL` et `no-new-privileges`. Il ne reçoit **aucune** capacité, y
compris pour le port 21 : la liaison privilégiée est faite par le démon Docker
via la publication de port, l'application restant sur le 2121 non privilégié.

### 7.5 Protections d'ingestion

- **Plafond par fichier** à chaque frontière (§5.5).
- **Noms de fichiers** — tout nom construit à partir d'une entrée contrôlée par
  l'émetteur (`PatientID` DICOM, dates d'examen, UID d'instance SOP,
  identifiant extrait d'un fichier constructeur) est réduit à un unique
  composant de chemin sûr avant d'atteindre le pipeline. Les écrivains de
  stockage et de quarantaine résolvent en outre le chemin final et refusent un
  résultat sortant de leur répertoire de base.
- **Échappement HL7** — les champs des requêtes `QRY^A19` sont échappés comme
  l'étaient déjà ceux du constructeur `ORU`. L'injection de segment était déjà
  refusée, mais `^`, `~`, `&` et `\` passaient : un identifiant portant `^`
  scindait QRD-8 en composants et le SIH lisait une autre requête que celle
  émise.
- **Temporisation progressive FTP** — les échecs d'authentification sont
  comptés par adresse source : les trois premiers sont servis à pleine vitesse,
  puis une seconde de délai par échec supplémentaire, plafonnée à 30 s, oubliée
  après 15 minutes de silence ou une authentification réussie. Le compteur est
  exporté (`ftp_auth_failures_total`). **Pas de verrouillage, délibérément** :
  derrière une traduction d'adresse tous les appareils partagent une même
  adresse, et un verrouillage permettrait à un scanner de mettre l'ingestion
  clinique hors service.
- **Inventaire des appareils par adresse MAC** — enrôlement manuel, révocation,
  et possibilité de refuser ce qui n'est pas identifiable. À ne pas compter
  comme un contrôle d'accès : inopérant sur réseau routé, et une adresse MAC
  s'usurpe (§3.2).
- **Protection contre la falsification de requête côté serveur** sur les URL de
  webhooks.

### 7.6 Traçabilité

Journal d'audit complet, consultable dans l'interface. Les actions sont
enregistrées sous le **nom d'utilisateur** et non sous un identifiant technique.
Pour les transactions IHE, l'acteur est le nom commun du certificat client
présenté.

Le HL7 ADT entrant consigne ce qui est arrivé et ce qu'il en a été advenu,
refus compris. **Aucun corps de message n'est conservé.**

### 7.7 Revues de sécurité

Deux revues internes, en juillet 2026. Les points relevés ont été corrigés et
intégrés : comportement en cas d'échec de détection du mode production, absence
de validation de `JWT_SECRET`, accessibilité de la page d'initialisation en
configuration OIDC exclusive, falsification de requête sur les webhooks, niveau
de journalisation LDAP exposant des données.

Le serveur **refuse de démarrer** sur un `JWT_SECRET` ou un
`AUTH_ENCRYPTION_KEY` faible ou de remplissage, sauf en
`APP_ENV=development`.

Procédure de signalement de vulnérabilité : [`SECURITY.md`](../SECURITY.md).

---

## 8. Données de santé

### 8.1 Nature des données

| Donnée | Emplacement |
|---|---|
| Tracés ECG d'origine | Volume `ecg` ou stockage objet |
| Identifiant, nom, date de naissance, sexe | Base de données |
| Métadonnées d'examen | Base de données |
| Fichiers rejetés à l'analyse | Volume `ecg-quarantine` |
| Traçabilité des accès | Base de données |
| Historique de livraison des webhooks | Base de données — **contient la charge utile complète, identifiants patient compris** |

### 8.2 Conservation

L'application fournit deux leviers :

- `storage.max_size` — plafond souple sur le volume ECG. **La rotation ne
  supprime aucun fichier clinique si elle n'est pas explicitement activée, et
  jamais en mode S3.** Par défaut, rien n'est supprimé.
- `WEBHOOKS_RETENTION_DAYS` — rétention de l'historique de livraison, 30 jours
  par défaut, `0` conservant indéfiniment. Compte tenu de la nature de ces
  lignes (§8.1), cette valeur mérite un arbitrage explicite.

Il n'existe pas de purge par ancienneté des dossiers patient : la conservation
des ECG est décidée par l'établissement et mise en œuvre sur le stockage (règles
de cycle de vie du *bucket* en mode S3).

### 8.3 Points relevant de l'établissement

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]**

- **Hébergement** — l'application n'impose aucun mode d'hébergement. Un
  déploiement traitant des données de santé à caractère personnel relève de la
  réglementation applicable à l'hébergement de données de santé ; la conformité
  porte sur l'infrastructure d'accueil, non sur le logiciel.
- **Localisation** des données et des sauvegardes.
- **Durée de conservation** des tracés, des fichiers en quarantaine, du journal
  d'audit et de l'historique des webhooks.
- **Base légale**, information des personnes, registre des traitements.
- **Analyse d'impact** relative à la protection des données, le cas échéant.
- **Niveau de journalisation en production.** Les journaux applicatifs peuvent
  contenir des identifiants patient ; leur rétention et leur accès relèvent de
  la politique de l'établissement. Ce point était resté ouvert à l'issue de la
  revue de juillet 2026.

---

## 9. Sauvegarde et restauration

### 9.1 Principe

La base de données et les fichiers ECG forment **un seul enregistrement**. Des
métadonnées sans leurs fichiers se restaurent en lignes pointant vers rien ; des
fichiers sans leurs lignes sont inatteignables. **Toute sauvegarde doit saisir
les deux au même point.**

### 9.2 Outillage fourni

| Script | Rôle |
|---|---|
| `scripts/backup.sh` | Sauvegarde la base (`pg_dump`, format *custom*) et les deux volumes, dans un répertoire horodaté. Rotation configurable (`KEEP`, 14 par défaut) |
| `scripts/restore.sh` | Restaure une sauvegarde. Destructif, exige une confirmation explicite |

Le répertoire n'est renommé à son nom définitif qu'une fois toutes les étapes
réussies : une sauvegarde partielle ne peut pas être prise pour une sauvegarde
complète, et un échec n'évince jamais une sauvegarde valide à la rotation.

L'ordre est délibéré — base d'abord, fichiers ensuite. Un fichier ingéré pendant
l'exécution se retrouve sur disque sans sa ligne : il est récupérable, car il
entre dans la file « non identifiés » à la réingestion. L'ordre inverse perdrait
le fichier.

```sh
# Sauvegarde
DATABASE_URL=postgres://... scripts/backup.sh /srv/backups/ecg-hub

# Restauration
DATABASE_URL=postgres://... scripts/restore.sh /srv/backups/ecg-hub/20261005-031500
```

Ces scripts couvrent une installation autonome. Lorsque la base est fournie par
l'établissement, **la sauvegarde appartient à ses mainteneurs** ; la contrainte
du §9.1 reste entière, et les volumes de fichiers doivent être saisis au même
point que le cliché de base.

### 9.3 Éléments à sauvegarder

| Élément | Impératif | Remarque |
|---|---|---|
| Base PostgreSQL | **Oui** | Métadonnées, identités, configuration, audit |
| Volume `ecg` | **Oui** | Les tracés eux-mêmes |
| `AUTH_ENCRYPTION_KEY` | **Oui** | Sans elle, les secrets en base sont illisibles. À conserver séparément |
| `JWT_SECRET` | Oui | Sa perte invalide les sessions en cours, sans autre conséquence |
| `config.yaml` | Oui | Petit, versionnable |
| Volume `ecg-quarantine` | Recommandé | Fichiers en attente d'arbitrage humain |
| Certificats `/certs` | Recommandé | Régénérables si la PKI est accessible |
| Images de conteneurs | Non | Reconstruites depuis le dépôt |

### 9.4 Objectifs de reprise

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]**

| | Valeur | Commentaire |
|---|---|---|
| RPO | *à définir* | Une sauvegarde quotidienne implique jusqu'à 24 h d'ECG perdus. Les appareils ne conservent généralement pas les examens déjà transmis : ce qui est perdu l'est définitivement. Une fréquence infra-quotidienne, ou l'archivage des journaux de transaction PostgreSQL, est à considérer |
| RTO | *à définir* | La restauration est séquentielle : `pg_restore` puis décompression des archives. Compter plusieurs heures sur une volumétrie annuelle (§5.4) |
| Fréquence | *à définir* | |
| Rétention | *à définir* | |
| Externalisation | *à définir* | |
| **Test de restauration** | *à définir* | Une sauvegarde jamais restaurée n'est pas une sauvegarde. Périodicité à inscrire au plan d'exploitation |

---

## 10. Supervision

### 10.1 Métriques

Le backend expose une quarantaine de métriques Prometheus sur le port 9091, lié
à la boucle locale par défaut.

Familles principales : ingestion (reçus, analysés, rejetés, mis en quarantaine,
par module), identité (requêtes HL7, réponses, rejets, épuisements de
tentatives), distribution (livraisons de webhooks, recopies vers connecteurs,
échecs), stockage (occupation du volume, file d'attente S3), sécurité (échecs
d'authentification FTP), et les métriques standard du moteur Go.

Tableau de bord Grafana fourni : `docs/grafana/ecg-hub-dashboard.json`, avec son
`prometheus.yml` et une pile de supervision prête à l'emploi
(`docker-compose.metrics.yml`).

### 10.2 Sondes

`/healthz` est public et sans authentification, destiné aux sondes
d'infrastructure. Le service de base de données déclare sa propre sonde
(`pg_isready`).

### 10.3 Journaux

Journaux structurés sur la sortie standard, collectés par le pilote de
journalisation de l'hôte. Niveau réglé par `LOG_LEVEL` (`info` par défaut).
Voir la réserve du §8.3 sur le contenu des journaux.

### 10.4 Alertes recommandées

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]** — seuils et destinataires.

| Condition | Pourquoi |
|---|---|
| Aucun ECG ingéré depuis *N* heures ouvrées | Panne silencieuse : un port fermé ou un module arrêté ne produit aucune erreur côté hub |
| Taux de mise en quarantaine en hausse | Changement de version du microprogramme d'un appareil, ou appareil mal configuré |
| File « non identifiés » en croissance | SIH injoignable, ou mappings de champs à revoir |
| Échecs de requête HL7 | Indisponibilité du SIH |
| Échecs de recopie vers connecteur | PACS injoignable |
| Occupation du volume ECG > 80 % | Saturation du stockage |
| File d'attente S3 qui ne se vide pas | Indisponibilité du stockage objet |
| Échecs d'authentification FTP en rafale | Balayage, ou appareil dont les identifiants ont changé |
| Certificat FTPS / DICOM TLS à moins de 30 jours | Expiration : tous les appareils en TLS tombent d'un coup |
| Sonde `/healthz` en échec | Indisponibilité |

---

## 11. Exploitation

### 11.1 Mise en service

Préparer `.env` à partir de `.env.example` et `config.yaml` à partir de
`config.example.yaml`, puis choisir le fichier de composition correspondant au
mode réseau retenu (§3.2) : `docker-compose.yml` en mode *bridge*,
`docker-compose.host.yml` en mode *host*.

Déploiement :

```sh
docker compose up -d --build
docker compose logs -f backend
```

Avec un coffre à secrets :

```sh
infisical run --env=prod -- docker compose up -d
```

Le backend énumère chaque variable qu'il lit sous `environment:` plutôt que de
charger un fichier, de sorte que les valeurs peuvent venir d'un coffre. Une
variable absente se résout en chaîne vide, et le serveur échoue immédiatement
sur celles qui ne doivent pas l'être.

Après le premier démarrage, se rendre sur l'interface, créer le compte
administrateur initial, puis configurer modules, HL7 et connecteurs.

### 11.2 Démarrage, arrêt, redémarrage

```sh
docker compose up -d        # démarrage
docker compose stop         # arrêt
docker compose restart backend
```

Un changement de mode réseau ou de port publié exige un `docker compose down`
préalable : ces modifications ne s'appliquent pas à une pile en fonctionnement.

Les modules constructeurs démarrent et s'arrêtent depuis l'interface
d'administration, sans toucher à la pile.

### 11.3 Mise à jour applicative

```sh
git fetch --tags && git checkout <tag>
scripts/backup.sh                      # avant toute chose
docker compose up -d --build
docker compose logs -f backend
```

Les migrations de schéma sont appliquées automatiquement au démarrage.

### 11.4 Retour arrière

```sh
git checkout <tag précédent>
docker compose up -d --build
```

> **Réserve.** Les migrations de schéma ne sont pas réversibles
> automatiquement : une version antérieure du code peut ne pas savoir ouvrir un
> schéma migré. Un retour arrière franchissant une migration passe par la
> restauration de la sauvegarde prise avant la mise à jour (§9.2) — d'où
> l'ordre imposé au §11.3.

### 11.5 Montée de version PostgreSQL

À traiter **avant** le changement d'image, et jamais en le découvrant en
production : PostgreSQL 18 ne peut pas ouvrir un cluster 16 et initialise à la
place un cluster vide. La pile démarre alors en bonne santé sur une base
blanche, tandis que les anciennes données restent intactes dans le volume —
panne silencieuse et parfaitement trompeuse. Procédure de vidage et de
rechargement : [`docs/POSTGRES_UPGRADE.md`](POSTGRES_UPGRADE.md).

### 11.6 Renouvellement des certificats

Les certificats des serveurs appareils sont lus depuis `/certs`. Ils sont
réécrits en place et le module est redémarré ; rien en base ne retient de
chemin. Avec certbot, un crochet de déploiement copie la paire et la confie à
l'uid 10001 — certbot écrit `live/` en liens symboliques vers `archive/`, en
`0700` et propriété de root, qu'un conteneur non privilégié ne peut pas suivre.
Crochet et détails : [`docs/TLS_CERTS.md`](TLS_CERTS.md).

### 11.7 Gestes courants

| Situation | Action |
|---|---|
| Fichier en quarantaine | Administration > Quarantaine : motif du rejet, réingestion ou suppression |
| ECG non identifié | File « non identifiés » : affectation manuelle, contrôlée par recoupement nom / date de naissance / identifiant, puis réingestion |
| Nouvel appareil | Administration > Appareils : enrôlement de l'adresse MAC (mode *host*), puis configuration de l'appareil avec l'adresse et le port publics |
| Changement d'adresse du SIH | Administration > HL7 : prise d'effet immédiate, sans redémarrage |
| Vérification d'un flux HL7 | Administration > HL7 : requête de test, et historique des messages reçus avec leur sort |

---

## 12. Continuité et reprise d'activité

### 12.1 Ce que l'application apporte

- **Perte de la base** — l'ingestion s'arrête. Les fichiers déjà sur le volume
  sont intacts et réingérables.
- **SIH injoignable** — l'ingestion continue. Les ECG s'accumulent en attente
  d'identification et sont enrichis quand le SIH répond. L'ordonnanceur
  réessaie, avec un état d'épuisement explicite. **Aucun ECG n'est perdu.**
- **PACS injoignable** — la recopie réessaie. L'ingestion n'est pas affectée.
- **Stockage objet injoignable** (mode S3) — les fichiers restent dans la file
  d'attente locale et sont téléversés au retour du service. **Aucun ECG n'est
  perdu**, dans la limite du volume local (§5.6).
- **Arrêt applicatif** — sur `SIGTERM` (`docker stop`, systemd), les modules
  d'ingestion sont arrêtés puis le serveur HTTP est vidé, avec un délai de 15 s.
  Les fichiers déjà reçus et en cours de traitement sont **enregistrés ou mis en
  quarantaine**, non perdus avec le processus.

  *Réserve :* le sort d'un transfert FTP encore en cours au moment du signal n'a
  pas été éprouvé sur le parc réel — la bibliothèque FTP peut couper la
  connexion. L'appareil doit alors retransmettre. La déduplication ci-dessous
  rend cette retransmission sans conséquence, mais un arrêt se planifie hors
  période d'acquisition.
- **Déduplication** sur l'empreinte SHA-256 du contenu : une retransmission du
  même fichier ne crée pas de doublon, ce qui rend toute reprise sans risque.

### 12.2 Ce qui relève de l'infrastructure

**[À COMPLÉTER PAR L'ÉTABLISSEMENT]**

L'application étant en instance unique (§3.3), la tolérance de panne de l'hôte
est à construire au niveau de l'infrastructure :

- Hôte virtualisé avec redémarrage automatique sur un autre nœud.
- Stockage redondé pour les volumes.
- Base en grappe, si fournie par l'établissement.
- Plan de bascule : le point sensible est **l'adresse réseau**. Les appareils
  sont configurés à la main avec une adresse ; une reprise doit la préserver
  (adresse virtuelle), sans quoi il faut repasser sur chaque appareil du site.

**Pendant une indisponibilité d'ECG Hub** : les appareils conservent
généralement les examens non transmis localement et les retransmettent ensuite,
mais cela dépend du modèle et de sa capacité. Ce comportement doit être vérifié
sur le parc réel, car il détermine ce qu'une panne coûte effectivement.

---

## 13. Interopérabilité

### 13.1 En tant que source d'information IHE

ECG Hub expose le profil **Retrieve ECG for Display** en acteur *Information
Source* : Retrieve ECG List [CARD-5], Retrieve ECG Document for Display
[CARD-6], et les deux `requestType` de synthèse de Retrieve Specific Information
for Display [ITI-11].

Un DPI liste les ECG d'un patient et ouvre un tracé sans quitter son écran et
sans intégration écrite pour ce hub en particulier.

Ces transactions vivent sur un **écouteur dédié, mutuellement authentifié**,
jamais sur l'API. Elles ne portent aucune authentification propre — le profil
l'attend d'un acteur ATNA groupé : mTLS constitue donc tout le modèle de
sécurité, et chacun de ses éléments est **refusé au démarrage plutôt que doté
d'une valeur par défaut**.

CARD-6 refuse un document qui ne porterait aucune identité patient : le §4.6.4.2.2.1
du profil n'autorise pas de document ECG anonyme sur cette transaction.

Outils : `tools/ihe-display.html` est un acteur *Display* de test et de
démonstration ; `scripts/gen-ihe-certs.sh` produit une PKI jetable pour
l'éprouver.

### 13.2 En tant que récepteur de mises à jour patient

**Patient Update [RAD-12]** — un écouteur MLLP entrant applique les mises à jour
démographiques `ADT^A08` et les fusions `ADT^A40`, avec les mêmes mappings de
champs que le chemin de requête.

**Désactivé par défaut** : il s'agit d'un chemin d'écriture dans l'identité
patient, atteint par le réseau.

Sémantique conforme au profil : un champ omis conserve sa valeur enregistrée, un
champ transmis à `""` est effacé. Un message rejoué plus ancien que le dernier
appliqué est ignoré — c'est ce qui empêche une retransmission de remettre en
place un nom vieux de trois semaines.

Restriction d'émetteurs disponible, par adresse ou par établissement émetteur
(MSH-4), **vide par défaut**. Réserve d'exploitation : derrière une traduction
d'adresse, la restriction par adresse ne distingue que la passerelle ; seul
MSH-4, qui voyage dans le message, reste exploitable. Détails :
[`docs/HL7_INBOUND_ADT.md`](HL7_INBOUND_ADT.md).

### 13.3 Autres interfaces

- **HL7 sortant** — `QRY^A19` pour l'enrichissement démographique, `ORU` pour
  la transmission de résultat.
- **DICOM** — C-STORE SCP en réception, SCU en recopie. Un connecteur convertit
  ce qu'il reçoit : un fichier constructeur accepté par le filtre d'extension
  était auparavant remis brut à l'association et refusé par le PACS — un échec
  signalé par le mauvais côté de la liaison.
- **Attente d'enrichissement** — un connecteur peut attendre l'enrichissement
  HL7 avant de transmettre, afin que le document porte les données
  démographiques de l'établissement et non ce que l'appareil a enregistré.
  L'attente se termine sur tout état terminal : un SIH qui ne répond jamais
  retarde une livraison, il ne l'annule pas.
- **API REST** — documentée, avec clés d'API.
- **Webhooks** — déclenchés après identification, avec historique de livraison.

### 13.4 Déclaration d'intégration IHE

**[À COMPLÉTER]** Aucune déclaration d'intégration IHE formelle n'est publiée à
ce jour, et aucun passage en Connectathon n'a eu lieu. L'implémentation suit la
spécification du profil et a été éprouvée contre l'acteur *Display* fourni avec
le dépôt. Un Connectathon est le moyen d'obtenir une déclaration opposable, si
l'établissement l'exige.

---

## 14. Limites connues

À jour pour la version 1.1.0.

- **Instance unique.** Pas de montée en charge horizontale, par conception
  (§3.3).
- **Pas de notion de site.** Un déploiement multi-établissements ne peut
  cloisonner ni les données ni les permissions par site. Voir le prérequis du §4.
- **Fichiers ECG non compressés.** La compression au niveau du système de
  fichiers ou du stockage est recommandée (§5.4).
- **Identification par adresse MAC inopérante sur réseau routé** — donc dès que
  les appareils et le serveur sont sur des VLAN distincts. C'est une limite de
  la couche 2, que ni le mode réseau ni l'orchestrateur ne lèvent (§3.2).
- **Fichiers FDA aECG XML de provenance tierce non identifiés
  automatiquement.** Le module lit l'identifiant patient dans un élément
  `PatientID` sous `subjectDemographicPerson`, qui ne fait pas partie du schéma
  HL7 aECG, au lieu de `trialSubject/id/@extension` où le schéma le place. Les
  fichiers exportés par un autre système atterrissent donc dans la file « non
  identifiés » pour affectation. **Sans effet sur l'ingestion des appareils** :
  les `.DAT` Nihon Kohden passent par leur propre module, qui lit l'identifiant
  correctement, et l'aECG XML produit par ce projet le porte aux deux endroits.
- **Pas de purge par ancienneté des dossiers patient** (§8.2).
- **HL7 MLLP sans TLS** : protection par le cloisonnement réseau uniquement
  (§7.2).
- **Pas de manifestes Kubernetes/Helm** (§3.4).
- **Pas de déclaration d'intégration IHE formelle** (§13.4).

---

## Annexe A — Variables d'environnement

### Obligatoires

| Variable | Rôle |
|---|---|
| `DATABASE_URL` | Chaîne de connexion. Atteignable **depuis le réseau des conteneurs** : le nom de service, non la boucle locale |
| `JWT_SECRET` | Signature des jetons de session. Valeur faible refusée au démarrage |
| `AUTH_ENCRYPTION_KEY` | Chiffrement AES-256-GCM des secrets en base. **Sa perte est irréversible** (§7.3) |
| `DB_USER`, `DB_PASSWORD`, `DB_NAME` | Uniquement avec la base embarquée |

### Principales variables optionnelles

| Variable | Défaut | Rôle |
|---|---|---|
| `APP_ENV` | — | Toute valeur autre que `development`/`dev`/`test`/`local` vaut production, où les contrôles de robustesse des secrets s'appliquent. **Laisser non défini en production** |
| `HOST_URL` | — | Origine publique : CORS et liens de rappel des webhooks |
| `LOG_LEVEL` | `info` | Niveau de journalisation |
| `CONFIG_PATH` | `/app/config.yaml` | Emplacement du fichier d'infrastructure |
| `FTP_PORT` | `21` | Port FTP côté hôte |
| `DICOM_PORT` | `4242` | Port DICOM côté hôte |
| `ADT_PORT` | `2576` | Port ADT entrant côté hôte |
| `DEVICE_WHITELIST` | `false` | Inventaire par adresse MAC. Sans effet hors mode *host*, et sans effet sur réseau routé (§3.2) |
| `METRICS_ENABLED` | `false` | Exposition Prometheus |
| `METRICS_PORT` | `9091` | Port de collecte |
| `TLS_CERT_FILE` | `/certs/fullchain.pem` | Certificat FTPS et DICOM TLS |
| `TLS_KEY_FILE` | `/certs/privkey.pem` | Clé correspondante |
| `STORAGE_BACKEND` | `local` | `local` ou `s3` |
| `S3_ENDPOINT`, `S3_BUCKET`, `S3_REGION`, `S3_PREFIX`, `S3_PATH_STYLE` | — | Stockage objet |
| `S3_ACCESS_KEY`, `S3_SECRET_KEY` | — | Identifiants du stockage objet. **Jamais dans `config.yaml`** |
| `WEBHOOKS_RETENTION_DAYS` | `30` | Rétention de l'historique de livraison. `0` conserve indéfiniment. Voir §8.1 |

Liste complète et commentée : `.env.example`.

---

## Annexe B — Points à instruire avec l'établissement

| Réf. | Sujet | Décideur |
|---|---|---|
| §3.2 | Mode réseau ; et si un filtrage matériel est exigé, adjacence de niveau 2 entre appareils et serveur à arbitrer | DSI / Sécurité |
| §4 | **Unicité de l'identifiant patient sur le périmètre — bloquant** | DSI / Identité patient |
| §5.6 | Volume quotidien, parc d'appareils, formats | Service biomédical |
| §6.3 | Adresses, VLAN, ouvertures de flux (dont la plage passive FTP) | Réseau |
| §7.2 | FTPS et DICOM TLS exigés ou non, fourniture des certificats | Sécurité / PKI |
| §8.3 | Hébergement, localisation, durées de conservation, journalisation | DPO / Juridique |
| §9.4 | RPO, RTO, fréquence, rétention, externalisation, test de restauration | Exploitation |
| §10.4 | Seuils d'alerte et destinataires | Exploitation |
| §12.2 | Tolérance de panne de l'hôte, adresse virtuelle de bascule | Infrastructure |
| §13.4 | Déclaration d'intégration IHE exigée ou non | DSI / Interopérabilité |

---

## Annexe C — Diagrammes

Diagrammes interactifs autonomes, dans `docs/diagrams/` (`index.html` les
rassemble) :

| Fichier | Contenu |
|---|---|
| `ecg-hub-system.html` | Architecture générale des composants |
| `ecg-hub-ingestion.html` | Chaîne d'ingestion, de la réception à la mise à disposition |
| `ecg-hub-flow.html` | Flux de données et d'identité |
| `ecg-hub-api-rbac.html` | Procédures de l'API et permissions associées |
| `ecg-hub-kubernetes.html` | Architecture Kubernetes envisagée (§3.4) |
