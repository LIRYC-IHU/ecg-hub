# Déploiement Kubernetes

Manifestes Kustomize bruts, sans Helm. Écrits et validés contre un cluster
**Talos v1.14.2 / Kubernetes v1.37** avec MetalLB et Traefik.

Ce répertoire couvre un déploiement d'essai. Pour la production, le chemin
documenté et éprouvé reste Docker Compose — voir [`docs/deploy-prod.md`](../../docs/deploy-prod.md)
et le dossier d'architecture [`docs/DAT.md`](../../docs/DAT.md).

## Ce que le cluster doit fournir

| Besoin | Pourquoi |
|---|---|
| `LoadBalancer` (MetalLB ou équivalent) | **Indispensable.** La plage `NodePort` par défaut est 30000-32767, ce qui exclut les ports 21 et 4242 — or les appareils Nihon Kohden composent le 21 et ne sont pas paramétrables |
| Contrôleur d'Ingress | Interface web |
| PostgreSQL 18 joignable | L'application ne l'embarque pas |
| Volumes NFS, ou une StorageClass | Voir « Stockage » ci-dessous |
| Registry d'images | Aucune image n'est publiée publiquement |

## Les deux contraintes propres à Talos

Elles ne viennent pas de Kubernetes mais de Talos, et il vaut mieux les
connaître avant de chercher la panne ailleurs.

**PodSecurity `baseline` est appliqué à tout le cluster.** Vérifiable :

```console
$ kubectl apply --dry-run=server -f - <<< '{"apiVersion":"v1","kind":"Pod",
  "metadata":{"name":"p","namespace":"default"},
  "spec":{"hostNetwork":true,"containers":[{"name":"c","image":"busybox"}]}}'
Error from server (Forbidden): violates PodSecurity "baseline:latest":
host namespaces (hostNetwork=true)
```

Sur kubeadm ou k3s, l'admission PodSecurity existe mais son défaut est
`privileged` : rien à faire. Sur Talos, deux choses sont refusées tant que le
namespace ne porte pas `pod-security.kubernetes.io/enforce=privileged` :
`hostNetwork` et les volumes `hostPath`. D'où, respectivement, l'absence du
filtrage par adresse MAC et le recours à des `PersistentVolume` déclarés.

**Pas de SSH, racine en lecture seule, `/var` seul chemin inscriptible.** La
redirection `21 → 2121` de `make ftp-ports` n'a pas d'équivalent : il n'y a pas
d'hôte où la poser. Le Service `LoadBalancer` fait le travail à sa place.

## Ce qui ne fonctionne pas ici, et pourquoi

**Le filtrage par adresse MAC des appareils** (`DEVICE_WHITELIST`). Le contrôle
lit la table de voisinage de l'hôte : il exige `hostNetwork` et un pod sur le
même segment de niveau 2 que les appareils. `hostNetwork` étant refusé par
PodSecurity `baseline`, la variable reste à `false`, explicitement, pour que
personne ne croie le contrôle actif.

Pour l'activer : un namespace étiqueté `privileged`, `hostNetwork: true`, et un
`nodeSelector` épinglant le pod sur un nœud raccordé au VLAN des appareils. Ce
n'est pas fourni ici — c'est un autre déploiement, pas une option.

## Déployer

### 1. Pousser les images

Aucune image n'est publiée : il faut les construire et les pousser. Les nœuds
Talos sont **amd64** et un Mac récent est arm64, d'où `--platform`, sans quoi
les pods échouent en `exec format error`.

```sh
cd <racine du dépôt>
REG=zot.waxmaker.fr/ecg-hub
TAG=1.1.0

docker login $REG

docker buildx build --platform linux/amd64 \
  -t $REG/backend:$TAG ./backend --push

docker buildx build --platform linux/amd64 \
  -t $REG/frontend:$TAG ./frontend --push
```

Si le registry demande une authentification depuis le cluster :

```sh
kubectl -n ecg-hub create secret docker-registry zot \
  --docker-server=zot.waxmaker.fr \
  --docker-username=<utilisateur> --docker-password=<mot de passe>
```

puis ajouter aux deux Deployments :

```yaml
      imagePullSecrets:
        - name: zot
```

### 2. Stockage

Renseigner `server` et `path` dans [`storage-nfs.yaml`](storage-nfs.yaml), pour
les deux volumes.

Le processus tourne en uid/gid 10001. Côté export, soit `chown 10001:10001` les
répertoires, soit s'appuyer sur le `fsGroup: 10001` du pod.

Autres options, si NFS n'est pas disponible :

- **`local-path-provisioner`**, dans son propre namespace `privileged` comme
  MetalLB. Son pod assistant crée les répertoires lui-même, ce qui règle le
  problème de l'absence de shell sur Talos. Le pointer vers un chemin sous
  `/var`, le seul inscriptible — son défaut `/opt/local-path-provisioner`
  échouera.
- **CSI Proxmox**, la solution propre pour de la production sur cette
  infrastructure.

Dans les deux cas, remplacer `storage-nfs.yaml` par un simple PVC avec
`storageClassName` et retirer les PV statiques.

### 3. Secrets

```sh
cp secret.example.yaml secret.yaml   # ignoré par git
# renseigner DATABASE_URL, JWT_SECRET, AUTH_ENCRYPTION_KEY
kubectl apply -f secret.yaml
```

`JWT_SECRET` et `AUTH_ENCRYPTION_KEY` : `openssl rand -base64 48` et
`openssl rand -base64 32`. Une valeur faible ou de remplissage fait **refuser le
démarrage** du serveur, ce qui est voulu.

> La perte de `AUTH_ENCRYPTION_KEY` rend illisible tout secret déjà enregistré
> en base. À sauvegarder avec la base, et à conserver ailleurs qu'à côté d'elle.

### 4. DNS

Le certificat est bien un wildcard (`Certificate/wildcard` de cert-manager,
servi par le `TLSStore` par défaut de Traefik), mais **le DNS ne l'est pas** :
chaque hôte a son propre enregistrement Cloudflare.

```console
$ dig +short nginx-k8s.waxmaker.fr      # hôte déclaré
104.21.12.115
$ dig +short test-quelconque.waxmaker.fr  # pas de wildcard
$
```

Créer l'enregistrement pour `ecg-k8s`, sur le même modèle que `nginx-k8s`
(proxifié par Cloudflare, vers la VM HAProxy qui sert Traefik). Sans lui,
l'Ingress fonctionne mais n'est atteignable qu'en forçant la résolution :

```sh
curl -k --resolve ecg-k8s.waxmaker.fr:443:10.10.50.100 \
  https://ecg-k8s.waxmaker.fr/
```

### 5. Appliquer

```sh
kubectl apply -k .
kubectl -n ecg-hub get pods,svc,pvc
kubectl -n ecg-hub logs -f deploy/backend
```

### 6. Configurer les modules

Rien de ce qui suit ne se règle dans un manifeste : modules, HL7, connecteurs,
webhooks et authentification vivent en base et se configurent dans l'interface.
`config.yaml` ne porte que l'infrastructure.

Sur https://ecg-k8s.waxmaker.fr, créer le compte administrateur initial, puis
**Administration > Modules** :

| Module | Port | Autres réglages |
|---|---|---|
| FTP | `2121` | **Public host** `10.10.50.101`, **Public port** `21`, plage passive `30000-30100` |
| DICOM | `4242` | — |
| ECTP | `30003` | — |

Trois pièges, tous silencieux :

- **La plage passive doit correspondre exactement** à celle de
  [`service-devices.yaml`](service-devices.yaml). Le serveur annonce un port
  dans sa réponse `227` ; un port non publié par le Service fait échouer le
  transfert juste après, sans erreur côté contrôle.
- **« Public host » doit porter l'adresse du LoadBalancer.** Sans elle, le
  serveur annonce son adresse de pod dans la réponse `227` — inatteignable
  depuis l'appareil.
- **`30003` est dans la plage passive mais absent de sa liste dans le Service**,
  car ECTP l'occupe : un port dupliqué fait **refuser le Service** par l'API
  Kubernetes. La plage reste 30000-30100 côté module, comme en Docker Compose :
  la bibliothèque FTP trouve 30003 pris et réessaie le port suivant.

## Tester le mode S3

Le mode objet n'est pas un stockage de remplacement : le volume local reste
utilisé comme file d'attente. Les fichiers sont écrits sur disque puis
téléversés par une tâche de fond, si bien que l'ingestion n'attend jamais le
réseau et qu'une indisponibilité du *bucket* coûte un retard, pas un ECG.

Dans [`files/config.yaml`](files/config.yaml), passer `storage.backend` à `s3`
et renseigner la section `s3`. Les identifiants n'y vont **jamais** : les
ajouter au Secret et au Deployment.

```yaml
# deployment-backend.yaml
            - { name: STORAGE_BACKEND, value: "s3" }
            - { name: S3_ENDPOINT, value: "s3.waxmaker.fr" }
            - { name: S3_BUCKET, value: "ecg-hub" }
            - { name: S3_PATH_STYLE, value: "true" }   # MinIO, RustFS, Ceph
            - name: S3_ACCESS_KEY
              valueFrom: { secretKeyRef: { name: ecg-hub, key: S3_ACCESS_KEY } }
            - name: S3_SECRET_KEY
              valueFrom: { secretKeyRef: { name: ecg-hub, key: S3_SECRET_KEY } }
```

Le volume reste monté dans les deux modes : en `s3`, `max_size` cesse d'être un
seuil de rotation et devient une alerte sur la file d'attente. La lecture
fonctionne indifféremment sur les deux dispositions, sans migration — un
basculement se teste donc sans rien déplacer.

## Vérifier

```sh
# Le LoadBalancer porte-t-il l'adresse attendue, port 21 inclus ?
kubectl -n ecg-hub get svc devices

# Depuis un poste du réseau 10.10.50.0/24 :
nc -vz 10.10.50.101 21
nc -vz 10.10.50.101 4242

# Dépôt FTP, en mode passif
curl -T ecg.xml --ftp-pasv ftp://<utilisateur>:<mot de passe>@10.10.50.101/
```

Avec `externalTrafficPolicy: Local`, MetalLB n'annonce l'adresse que depuis le
nœud qui porte un pod prêt. Une adresse injoignable alors que le Service
l'affiche signifie presque toujours que le pod n'est pas `Ready` — regarder les
logs du backend avant de soupçonner MetalLB.

## Ce que ces manifestes ne font pas

- **Un seul réplica de backend**, et `strategy: Recreate`. Les appareils
  poussent vers une adresse unique, le pipeline d'ingestion est en mémoire, et
  une connexion de contrôle FTP et sa connexion de données doivent atteindre le
  même pod. La montée en charge est verticale.
- **Pas de HL7 ADT entrant.** Le port 2576 est commenté dans
  `service-devices.yaml` : c'est un chemin d'écriture dans l'identité patient,
  atteint par le réseau. Voir [`docs/HL7_INBOUND_ADT.md`](../../docs/HL7_INBOUND_ADT.md).
- **Pas de listener IHE.** Il exige une authentification mutuelle et une PKI, et
  refuse de démarrer sur une configuration incomplète. Voir `config.example.yaml`.
- **Pas de FTPS ni de DICOM TLS.** Monter la paire de certificats en Secret sur
  `/certs` et l'activer dans les modules.
- **Pas de sauvegarde.** `scripts/backup.sh` suppose un accès aux volumes ; en
  Kubernetes, le faire tourner en `CronJob` montant les mêmes PVC, ou sauvegarder
  depuis la couche de stockage. La base et les fichiers doivent être saisis au
  même point — voir §9 du DAT.
- **Pas de Helm.** Volontairement : ces manifestes servent à établir ce qui
  fonctionne réellement. Un chart viendra ensuite, avec les constats.
