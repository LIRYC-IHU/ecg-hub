# Changelog

All notable changes to this project are documented here. Versions follow
[semantic versioning](https://semver.org/).

## [1.0.0] — 2026-09-02

First release.

### Ingestion

- FTP/FTPS server (explicit AUTH TLS, mandatory encryption when TLS is on),
  DICOM C-STORE SCP with optional DICOM TLS, and an ECTP listener for Nihon
  Kohden devices. No agent is installed on the device.
- Vendor modules for Philips, GE MUSE, Nihon Kohden, Mindray, FDA aECG XML and
  DICOM waveform, each validating and parsing into a common model. Modules
  start and stop from the admin UI without restarting the server.
- Files that cannot be parsed go to quarantine; files that parse without a
  usable patient ID enter the `unidentified` workflow.
- Deduplication on the SHA-256 of the file content.

### Patient identity

- Assignment of unidentified ECGs to a patient, guarded by name, date-of-birth
  and ID cross-checks, followed by re-ingestion.
- HL7 scheduler querying the HIS to enrich pending ECGs, with retries, an
  exhausted state and MSA rejection reporting.

### Storage

- Originals on a dedicated volume, metadata in PostgreSQL 18, quarantine on a
  separate volume.
- Optional S3-compatible object storage. Files are written locally first and
  uploaded from the spool, so a bucket outage delays uploads instead of
  refusing ingestion. Reads work across both layouts with no migration.
- Janitor with a soft size cap that never deletes clinical files unless
  rotation is explicitly enabled, and never in S3 mode.

### Distribution

- Proxy connectors forwarding a copy of each ECG to external systems (PACS over
  DICOM C-STORE, ECTP/FTP endpoints) with retries.
- Webhooks fired after identification, with delivery history and configurable
  retention.
- Batch export to ZIP in the formats each module can convert to.

### Web UI

- Master–detail patient and ECG browsing with a WebGL 12-lead waveform viewer.
- Admin pages for modules, HL7, connectors, webhooks, API keys, users, roles,
  auth providers, audit trail and system status.
- English and French.

### Security

- Local, OIDC and LDAP authentication; role-based permissions enforced per
  procedure; API keys.
- Provider and module credentials stored AES-256-GCM-encrypted.
- Full audit trail.
- FTPS and DICOM TLS read certificates mounted from the host, so renewal stays
  with whoever already does it. Enabling TLS without a usable certificate is
  refused at start-up with a stated reason, rather than accepted and then
  failing every client.

### Operations

- Bridge networking with published device ports; FTP published on the host as
  `${FTP_PORT:-21}` while the container binds 2121 unprivileged.
- Passive port range of 100 ports, sized so concurrency is not the ceiling.
- Around 40 Prometheus metrics and a Grafana dashboard.
- `loadtest/capacity-probe.sh` reports the CPU cost of a single ECG, measured
  at ~27 ms on a 2-core deployment.

### Known limitations

- **Third-party FDA aECG XML files are not auto-identified.** The `fda` module
  reads the patient identifier from a `PatientID` element under
  `subjectDemographicPerson`, which is not part of the HL7 aECG schema, instead
  of `trialSubject/id/@extension` where the schema puts it. Files exported by
  another system therefore land in the `unidentified` queue for an operator to
  assign. This does not affect device ingestion: Nihon Kohden `.DAT` files go
  through the `nihon-kohden` module, which reads the identifier correctly, and
  the aECG XML this project generates carries it in both places. The fix
  belongs to `ecg-bridge`.
- ECG files are stored uncompressed. XML formats compress to roughly a quarter
  of their size, so filesystem- or storage-level compression is worth enabling
  where the deployment allows it.
- No notion of site or establishment: a multi-hospital deployment cannot
  partition data or permissions per site.
