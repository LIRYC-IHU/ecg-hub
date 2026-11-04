# Déploiement Kubernetes — chart Helm

Chart issu de manifestes validés sur un cluster Kubernetes réel (Talos, MetalLB,
Traefik). Les valeurs par défaut sont génériques ; celles d'un site (registry,
hôte, adresses, stockage) vont dans un fichier à part, non suivi par git :

```sh
helm upgrade --install ecg-hub deploy/helm/ecg-hub \
  -n ecg-hub --create-namespace -f values.local.yaml
```

`values.local.yaml` est ignoré par git (`.gitignore` racine) et exclu du paquet
du chart (`.helmignore`).

Les noms d'objets sont fixes (`backend`, `frontend`, `devices`, …) : nginx
proxifie vers `backend:4444` en dur. Une seule release par namespace.

Pour la production, le chemin documenté et éprouvé reste Docker Compose — voir
[`docs/deploy-prod.md`](../../../docs/deploy-prod.md) et le dossier
d'architecture [`docs/DAT.md`](../../../docs/DAT.md).

## Ce que le cluster doit fournir

| Besoin | Pourquoi |
|---|---|
| `LoadBalancer` (MetalLB ou équivalent) | **Indispensable.** La plage `NodePort` par défaut est 30000-32767, ce qui exclut les ports 21 et 4242 — or certains électrocardiographes composent le 21 et ne sont pas paramétrables |
| Contrôleur d'Ingress | Interface web |
| PostgreSQL 18 joignable | L'application ne l'embarque pas |
| Une StorageClass, ou des volumes NFS | Voir « Stockage » ci-dessous |
| Registry d'images | Aucune image n'est publiée publiquement |

## PodSecurity

Le chart fonctionne sous PodSecurity `baseline` : il n'utilise ni `hostNetwork`
ni volume `hostPath`. Certaines distributions, Talos par exemple, appliquent
`baseline` à tout le cluster ; vérifiable ainsi :

```console
$ kubectl apply --dry-run=server -f - <<< '{"apiVersion":"v1","kind":"Pod",
  "metadata":{"name":"p","namespace":"default"},
  "spec":{"hostNetwork":true,"containers":[{"name":"c","image":"busybox"}]}}'
Error from server (Forbidden): violates PodSecurity "baseline:latest":
host namespaces (hostNetwork=true)
```

Pas `restricted` : l'image frontend est un `nginx:alpine` qui démarre en root
pour lier le port 80.

Sur un hôte Docker, la redirection `21 → 2121` est posée par `make ftp-ports`.
En Kubernetes, le Service `LoadBalancer` fait ce travail (port 21 vers 2121).

## Identification des appareils par adresse MAC

Désactivée d'office : le chart pose `DEVICE_WHITELIST=false`, et ce n'est pas
une valeur réglable. Les permissions `device.*` sont donc masquées et la page
Appareils n'apparaît pas. Ce qui suit explique pourquoi elle ne peut pas
fonctionner sous ce chart.

L'identification lit une table ARP. Une table ARP ne contient que des voisins du
même segment de niveau 2 : si les appareils sont sur un autre sous-réseau que
les nœuds, le trafic est routé, aucun nœud n'a d'entrée ARP pour l'appareil, et
la seule adresse matérielle visible serait celle du routeur — que le code refuse
de rendre (`ErrSharedHop`), parce que l'approuver approuverait tout ce qui se
trouve derrière.

`hostNetwork` n'est **pas** la clé. Le résolveur sait lire un `/proc/net` monté
depuis l'hôte (`HOST_PROC_NET`), ce qui est un `hostPath` — refusé par
`baseline`. Mais sans adjacence de niveau 2, la table ARP du nœud ne contient
pas davantage l'appareil. Pour que la fonction opère, il faut raccorder les
nœuds au réseau des appareils : c'est un plan d'adressage, pas une option du
chart.

Activée sans cette adjacence, elle journaliserait un avertissement par
connexion et laisserait tout passer — un contrôle qui n'interdit rien. Avec
`deny_unidentified`, elle refuserait **tous** les appareils et interromprait
l'ingestion clinique.

À lire comme un inventaire d'appareils, et non comme un contrôle d'accès : une
adresse MAC s'usurpe.

## Déployer

### 1. Pousser les images

Aucune image n'est publiée : il faut les construire et les pousser. Construire
pour l'architecture des nœuds (`linux/amd64` le plus souvent) ; depuis un Mac
arm64, sans `--platform`, les pods échouent en `exec format error`.

```sh
cd <racine du dépôt>
REG=<registry>/ecg-hub
TAG=<version>

docker login <registry>

docker buildx build --platform linux/amd64 \
  -t $REG/backend:$TAG ./backend --push

docker buildx build --platform linux/amd64 \
  -t $REG/frontend:$TAG ./frontend --push
```

Puis `backend.image` et `frontend.image` (`repository`, `tag`).

Si le registry exige une authentification depuis le cluster :

```sh
kubectl -n ecg-hub create secret docker-registry regcred \
  --docker-server=<registry> \
  --docker-username=<utilisateur> --docker-password=<mot de passe>
```

et `imagePullSecrets: [{ name: regcred }]`.

### 2. Stockage

`persistence.mode` :

| Mode | Usage |
|---|---|
| `storageClass` (défaut) | PVC dynamiques. `persistence.<volume>.storageClass` vide = StorageClass par défaut du cluster |
| `nfs` | Cluster sans StorageClass : PersistentVolume statiques + PVC liés. Renseigner `persistence.<volume>.nfs.server` et `.path` |
| `existingClaim` | PVC créés ailleurs : `persistence.<volume>.existingClaim` |
| `emptyDir` | Aucun volume persistant, **stockage S3 uniquement** (refusé sinon). Voir « Stockage objet » |

`<volume>` vaut `ecg` ou `quarantine`. PV et PVC portent
`helm.sh/resource-policy: keep` : `helm uninstall` ne les supprime pas, et les
PV statiques sont en `Retain`.

**Réinstaller après suppression du namespace (mode `nfs`).** Les PV survivent,
mais passent en `Released` : ils gardent l'UID de l'ancienne PVC, et les
nouvelles restent `Pending` — le backend avec elles. Retirer cet UID ; le nom
de la PVC reste dans la référence, donc le PV ne se lie qu'à elle :

```sh
for pv in ecg-hub-ecg ecg-hub-quarantine; do
  kubectl patch pv $pv --type json -p '[
    {"op":"remove","path":"/spec/claimRef/uid"},
    {"op":"remove","path":"/spec/claimRef/resourceVersion"}]'
done
```

Les fichiers ne bougent pas : ils sont sur l'export NFS.

Le processus tourne en uid/gid 10001. Côté export NFS, soit
`chown 10001:10001` les répertoires, soit s'appuyer sur le `fsGroup: 10001` du
pod.

Sans NFS ni StorageClass, `local-path-provisioner` dépanne pour un essai. Sur
une distribution à racine en lecture seule (Talos), le pointer vers un chemin
sous `/var`.

### 3. Secrets

Le chart ne crée pas le Secret par défaut : des secrets dans un fichier de
valeurs finissent dans git ou dans l'historique Helm. Le créer à part, sous le
nom `secret.name` (`ecg-hub`) :

```sh
kubectl -n ecg-hub create secret generic ecg-hub \
  --from-literal=DATABASE_URL='postgres://<utilisateur>:<mot de passe>@<hôte>:5432/<base>?sslmode=disable' \
  --from-literal=JWT_SECRET="$(openssl rand -base64 48)" \
  --from-literal=AUTH_ENCRYPTION_KEY="$(openssl rand -base64 32)"
# mode S3 : ajouter S3_ACCESS_KEY et S3_SECRET_KEY
```

Une base hors cluster s'expose par un Service sans selector (et ses
Endpoints) ; son nom pleinement qualifié va alors dans `DATABASE_URL`. Vérifier
le nom de la base plutôt que le supposer : une base absente ne se voit qu'au
démarrage, en `SQLSTATE 3D000`.

Pour un essai seulement : `secret.create: true` et `secret.values`.

Une valeur faible ou de remplissage pour `JWT_SECRET` ou `AUTH_ENCRYPTION_KEY`
fait **refuser le démarrage** du serveur, ce qui est voulu.

> La perte de `AUTH_ENCRYPTION_KEY` rend illisible tout secret déjà enregistré
> en base. À sauvegarder avec la base, et à conserver ailleurs qu'à côté d'elle.

### 4. DNS et TLS

`ingress.host` doit résoudre vers le contrôleur d'Ingress. Un certificat
wildcard ne suffit pas si le DNS, lui, n'a pas de wildcard : créer
l'enregistrement de l'hôte. Avant cela, tester en forçant la résolution :

```sh
curl -k --resolve <hôte>:443:<IP du contrôleur d'Ingress> https://<hôte>/
```

`ingress.tls` reste vide si le contrôleur sert déjà le certificat (certificat
par défaut, wildcard) ; sinon y déclarer le Secret TLS.

### 5. Installer

```sh
helm upgrade --install ecg-hub deploy/helm/ecg-hub \
  -n ecg-hub --create-namespace -f values.local.yaml
kubectl -n ecg-hub get pods,svc,pvc
kubectl -n ecg-hub logs -f deploy/backend
```

Le chart ne crée pas le namespace, et ne le peut pas : Helm enregistre la
release dans le namespace avant de créer le moindre objet, si bien qu'un
Namespace dans le chart échoue en `namespaces "…" not found`. Les Secrets
doivent de toute façon y exister avant le premier démarrage, donc le créer
d'abord, avec ses libellés PodSecurity :

```sh
kubectl create namespace ecg-hub
kubectl label namespace ecg-hub pod-security.kubernetes.io/enforce=baseline \
  pod-security.kubernetes.io/audit=restricted pod-security.kubernetes.io/warn=restricted
```

`--create-namespace` convient aussi, sans libellés ; côté ArgoCD,
`CreateNamespace=true` et `managedNamespaceMetadata`.

Avec ArgoCD, une `Application` pointant sur `deploy/helm/ecg-hub` ; les valeurs
du site en `valuesObject` (ou dans un dépôt de configuration à part) :

```yaml
spec:
  source:
    repoURL: <dépôt>
    path: deploy/helm/ecg-hub
    helm:
      valuesObject:
        ingress: { host: <hôte> }
        # ...
  destination:
    namespace: ecg-hub
  syncPolicy:
    syncOptions: [CreateNamespace=true]
    managedNamespaceMetadata:
      labels:
        pod-security.kubernetes.io/enforce: baseline
```

**Reprendre un déploiement existant** (objets créés par `kubectl apply`) : Helm
refuse d'adopter des objets qu'il n'a pas créés ; ArgoCD, lui, les adopte. Avec
Helm seul, annoter chaque objet existant :

```sh
for r in deploy/backend deploy/frontend svc/backend svc/frontend svc/devices \
         ingress/ecg-hub pvc/ecg-hub-ecg pvc/ecg-hub-quarantine; do
  kubectl -n ecg-hub annotate $r meta.helm.sh/release-name=ecg-hub \
    meta.helm.sh/release-namespace=ecg-hub --overwrite
  kubectl -n ecg-hub label $r app.kubernetes.io/managed-by=Helm --overwrite
done
# idem, sans -n, pour les PV statiques en mode nfs
```

### 6. Configurer les modules

Rien de ce qui suit ne se règle dans le chart : modules, HL7, connecteurs,
webhooks et authentification vivent en base et se configurent dans l'interface.
`config.yaml` ne porte que l'infrastructure.

Sur `https://<hôte>`, créer le compte administrateur initial, puis
**Administration > Modules** :

| Module | Port | Autres réglages |
|---|---|---|
| FTP | `2121` | **Public host** = adresse du Service `devices`, **Public port** `21`, plage passive = `devices.ftp.passive` |
| DICOM | `4242` | — |
| ECTP | `30003` | — |

Trois pièges, tous silencieux :

- **La plage passive doit correspondre exactement** à `devices.ftp.passive`.
  Le serveur annonce un port dans sa réponse `227` ; un port non publié par le
  Service fait échouer le transfert juste après, sans erreur côté contrôle.
- **« Public host » doit porter l'adresse du LoadBalancer.** Sans elle, le
  serveur annonce son adresse de pod dans la réponse `227` — inatteignable
  depuis l'appareil.
- **Un port de la plage déjà publié** (ECTP `30003` avec la plage par défaut)
  est retiré du Service par le chart : un doublon fait **refuser le Service**
  par l'API Kubernetes. Côté module, la plage reste entière : la bibliothèque
  FTP trouve le port pris et passe au suivant.

## Stockage objet (S3)

Le mode objet n'est pas un stockage de remplacement : le volume local reste
utilisé comme file d'attente. Les fichiers sont écrits sur disque puis
téléversés par une tâche de fond, si bien que l'ingestion n'attend jamais le
réseau et qu'une indisponibilité du *bucket* coûte un retard, pas un ECG.

Le stockage se règle dans `config.yaml` : [`files/config.yaml`](files/config.yaml),
ou la valeur `config` pour le remplacer en entier — `storage.backend` à `s3`
et la section `s3`. Les identifiants n'y vont **jamais** : `S3_ACCESS_KEY` et
`S3_SECRET_KEY` dans le Secret, que le Deployment lit déjà (clés optionnelles).

Ne pas déclarer `STORAGE_BACKEND` ni les autres `S3_*` dans
`backend.extraEnv` : le loader les lit avec `os.LookupEnv`, et une variable
déclarée, même vide, écrase la valeur du fichier.

Vérifier depuis un pod que l'endpoint est joignable : un nom qui résout vers une
adresse VPN ou externe peut être inaccessible depuis les nœuds. Une erreur
d'endpoint fait échouer le démarrage (`s3 bucket "…" unreachable`), sans
dégradation silencieuse.

Le volume reste monté dans les deux modes : en `s3`, `max_size` cesse d'être un
seuil de rotation et devient une alerte sur la file d'attente.

**En S3, faut-il un volume persistant ?** Pas forcément : `persistence.mode:
emptyDir` n'en monte aucun, les deux répertoires vivent dans le pod. Le prix :
un fichier reçu mais pas encore téléversé est **perdu** si le pod disparaît
avant l'envoi. En temps normal la fenêtre est de quelques secondes
(`upload_interval_seconds`) ; pendant une coupure du bucket, toute la file
d'attente est exposée. Et le backend refuse de démarrer quand le bucket est
injoignable : un pod redémarré pendant la coupure ne reprend pas avant son
retour. Un volume persistant, même petit, garde la file d'attente. La lecture
fonctionne indifféremment sur les deux dispositions, sans migration — un
basculement se teste donc sans rien déplacer.

## Vérifier

```sh
# Le LoadBalancer porte-t-il l'adresse attendue, port 21 inclus ?
kubectl -n ecg-hub get svc devices

# Depuis un poste du réseau des appareils :
nc -vz <adresse devices> 21
nc -vz <adresse devices> 4242

# Dépôt FTP, en mode passif
curl -T ecg.xml --ftp-pasv ftp://<utilisateur>:<mot de passe>@<adresse devices>/
```

Avec `externalTrafficPolicy: Local` et MetalLB en L2, l'adresse n'est annoncée
que depuis le nœud qui porte un pod prêt. Une adresse injoignable alors que le
Service l'affiche signifie presque toujours que le pod n'est pas `Ready` —
regarder les logs du backend avant de soupçonner le LoadBalancer.

## Ce que ce chart ne fait pas

- **Un seul réplica de backend** (`backend.replicas: 1`), et
  `strategy: Recreate`. Les appareils poussent vers une adresse unique, le
  pipeline d'ingestion est en mémoire, et une connexion de contrôle FTP et sa
  connexion de données doivent atteindre le même pod. La montée en charge est
  verticale.
- **Pas de HL7 ADT entrant** par défaut. `devices.adt.enabled` publie le port
  2576, à activer aussi dans `config.yaml` : c'est un chemin d'écriture dans
  l'identité patient, atteint par le réseau. Voir
  [`docs/HL7_INBOUND_ADT.md`](../../../docs/HL7_INBOUND_ADT.md).
- **Pas de listener IHE.** Il exige une authentification mutuelle et une PKI, et
  refuse de démarrer sur une configuration incomplète. Voir `config.example.yaml`.
- **Pas de FTPS ni de DICOM TLS.** Monter la paire de certificats en Secret sur
  `/certs` et l'activer dans les modules.
- **Pas de sauvegarde.** `scripts/backup.sh` suppose un accès aux volumes ; en
  Kubernetes, le faire tourner en `CronJob` montant les mêmes PVC, ou sauvegarder
  depuis la couche de stockage. La base et les fichiers doivent être saisis au
  même point — voir §9 du DAT.
