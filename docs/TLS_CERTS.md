# TLS for the device-facing servers (FTPS, DICOM TLS)

ECG Hub reads one certificate pair from a mounted directory:

```
/certs/fullchain.pem
/certs/privkey.pem
```

Those are certbot's filenames, and they are the defaults. Nothing about
certificates is stored in the database or configurable from the admin UI: the
department that already manages certificates keeps managing them, and renewal
never involves the application beyond a restart.

Override the paths with `TLS_CERT_FILE` / `TLS_KEY_FILE` when the layout differs
(an internal PKI, a wildcard shared with nginx, `server.crt`/`server.key`).

```yaml
backend:
  environment:
    TLS_CERT_FILE: /certs/fullchain.pem
    TLS_KEY_FILE: /certs/privkey.pem
  volumes:
    - ./certs:/certs:ro
```

Then enable TLS per module in **Admin → Modules**. The switch is disabled, with
the reason, while no usable pair is readable.

## Using certbot: two traps, one fix

Mounting `/etc/letsencrypt/live/<domain>` straight into the container **does not
work**, for two independent reasons.

**The files are symlinks.** `live/<domain>/fullchain.pem` points at
`../../archive/<domain>/fullchain1.pem`, which is outside the mount, so inside
the container the link dangles:

```
/certs $ ls -la
lrwxrwxrwx  fullchain.pem -> ../../archive/ftp.example.org/fullchain1.pem
/certs $ cat fullchain.pem
cat: can't open 'fullchain.pem': No such file or directory
```

**The permissions are root-only.** Mounting all of `/etc/letsencrypt` fixes the
symlinks and hits the second wall: `live/` and `archive/` are `0700 root`, and
`privkey1.pem` is `0600 root`. The backend runs as an unprivileged user
(uid 10001), so it cannot traverse those directories, let alone read the key.

Loosening those permissions puts a private key within reach of every process on
the host. Copy the pair out instead, with a certbot deploy hook — which also
gives you the restart for free on every renewal.

`/etc/letsencrypt/renewal-hooks/deploy/ecg-hub.sh`, `chmod 0700`:

```sh
#!/bin/sh
set -eu

DOMAIN=ftp.example.org
STACK=/opt/ecg-hub          # directory holding docker-compose.yml
DEST="$STACK/certs"
APP_UID=10001               # the "app" user inside the backend image
APP_GID=10001

# certbot sets RENEWED_LINEAGE when renewing one lineage; ignore the others.
# Unset means someone ran the hook by hand, which is how it is installed.
if [ -n "${RENEWED_LINEAGE:-}" ] && [ "$RENEWED_LINEAGE" != "/etc/letsencrypt/live/$DOMAIN" ]; then
  exit 0
fi

install -d -m 0755 "$DEST"
install -m 0644 -o "$APP_UID" -g "$APP_GID" "/etc/letsencrypt/live/$DOMAIN/fullchain.pem" "$DEST/fullchain.pem"
install -m 0640 -o "$APP_UID" -g "$APP_GID" "/etc/letsencrypt/live/$DOMAIN/privkey.pem"   "$DEST/privkey.pem"

# The servers load the pair at start, so a renewed certificate only takes
# effect after a restart.
cd "$STACK" && docker compose restart backend
```

`install` follows the symlinks and copies contents, so the archive layout stays
where it belongs. Run it once by hand to seed `certs/`, then certbot runs it on
every renewal.

Check the uid your image actually uses before trusting the numbers above:

```bash
docker compose exec backend id
```

## Verifying

Certificate and chain, without needing an FTP account — `Verify return code: 0`
is the whole point, and it only holds if the chain the server sends is complete:

```bash
openssl s_client -connect ftp.example.org:2121 -starttls ftp \
  -servername ftp.example.org </dev/null 2>&1 \
  | grep -E "subject=|issuer=|Verify return code|Protocol|Cipher"
```

A real upload, encrypted end to end:

```bash
curl -sS -v --ssl-reqd -u user:pass -T sample.xml \
  ftp://ftp.example.org:2121/sample.xml 2>&1 \
  | grep -E "230|PBSZ|PROT|229|150|226"
```

What that output must contain:

| line | meaning |
|---|---|
| `234 AUTH command ok` | explicit FTPS accepted on the control channel |
| `PROT P` → `200` | **the data channel is encrypted too** — without it the file crosses in clear despite a successful AUTH TLS |
| `229 Entering Extended Passive Mode (\|\|\|30005\|)` | passive port chosen from the configured range |
| `Connecting to <public IP>` | `public_host` is right; an internal address here means transfers hang for outside clients |
| `226 Closing transfer connection` | transfer complete |

Never pass `--insecure` against a real deployment: its absence is what proves
the chain. Keep it for a self-signed pair in development.

`loadtest/ftp-stress.sh` does all of this in bulk, including a certificate
preflight, and defaults to TLS.

## Renewal

Nothing to do beyond the hook. Note two things:

- The servers load the certificate **at start**, so a renewed pair is only
  served after the module restarts. The hook handles it.
- An expired certificate is treated as unusable and the module refuses to
  start, rather than serving it and failing devices with an opaque handshake
  error. The admin screen shows the expiry date, in orange under 30 days —
  certbot's own renewal window, so an approaching date means renewal has
  stopped running.
