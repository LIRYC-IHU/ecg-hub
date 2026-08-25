# Security Policy

ECG Hub handles patient recordings and identity data from hospital systems. A
vulnerability here is a patient-data problem, not just a software bug — please
treat it accordingly.

## Reporting a vulnerability

**Do not open a public issue for a security problem.**

Use GitHub's private reporting instead: **Security → Advisories → Report a
vulnerability** on this repository. It opens a channel visible only to you and
the maintainers.

Useful in a report, in rough order of value:

- what an attacker can reach (data, account, host) and from where — unauthenticated, any logged-in user, a specific permission;
- the smallest reproduction you have: request, payload, or steps in the UI;
- the version or commit you tested;
- your deployment shape if it matters (behind nginx, Traefik, OIDC/LDAP, exposed FTP/DICOM ports).

Proof-of-concept code is welcome; a working exploit is not required.

### What to expect

- **Acknowledgement within 5 working days.** We are a hospital research team, not a 24/7 security desk — if it is silent longer than that, ping the advisory thread.
- An assessment and a fix plan on the same thread, including whether we consider it a vulnerability and why.
- Credit in the advisory and the release notes, unless you prefer not to be named.

We have no bug-bounty programme.

## Scope

In scope: anything in this repository — the Go backend, the React frontend, the
ingestion modules, the deployment manifests and the default configuration.

Out of scope, but still worth telling us about:

- vulnerabilities in a deployment's own infrastructure (reverse proxy, database, identity provider) rather than in this code;
- findings that require an already-compromised host or database;
- missing hardening headers on endpoints served by the operator's own proxy (see `docs/deploy-prod.md` for what the proxy is expected to add).

## Supported versions

The project has no release tags yet: **`main` is the supported branch**, and
fixes land there. Sites running a pinned commit should expect to rebase onto
`main` to receive a fix. This section will be replaced by a version table when
tagged releases start.

## Security model in one screen

Context that usually saves a round-trip when judging a report:

- **Authentication.** Local accounts (bcrypt) or an identity provider — OIDC (Keycloak, Authentik) and LDAP are both supported. Sessions are HttpOnly JWT cookies with sliding refresh; a role change invalidates every session of that user.
- **Authorisation.** Every route requires an explicit permission. There is no implicit admin bypass: the `admin` role is a row in the database like any other, and can be renamed or removed.
- **Machine access.** API keys (`ecghub_…`) are stored as SHA-256 hashes, inherit their owner's role, and are checked on every request. They are shown once, at creation.
- **Secrets at rest.** Webhook signing secrets, outbound `Authorization` headers and identity-provider client secrets are AES-256-GCM encrypted with `AUTH_ENCRYPTION_KEY` and never returned by the API.
- **Outbound webhooks.** Payloads are signed with HMAC-SHA256. Delivery has a dial-time SSRF guard: loopback, link-local, cloud-metadata and multicast targets are refused after DNS resolution, and redirects are not followed. RFC1918 targets are allowed on purpose — internal research servers are the normal case.
- **Production guards.** The server refuses to start with the placeholder `JWT_SECRET`; `APP_ENV` must be set explicitly to opt out of production defaults.
- **Auditing.** Logins, role changes, downloads, deletions, quarantine assignments and webhook changes are written to an audit log readable from the UI.

Please say so in your report if a finding contradicts any of the above — that is
exactly the kind we want to hear about first.
