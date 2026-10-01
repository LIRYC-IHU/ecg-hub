# Changelog

All notable changes to this project are documented here. Versions follow
[semantic versioning](https://semver.org/).

## [1.1.0] — 2026-10-01

Interoperability release. The hub now answers to a DPI over IHE, is pushed
patient updates by the HIS, and proxies to a PACS whatever vendor format it was
given.

### Interoperability — IHE

- **Retrieve ECG for Display** as an Information Source: Retrieve ECG List
  [CARD-5], Retrieve ECG Document for Display [CARD-6] and the two summary
  requestTypes of Retrieve Specific Information [ITI-11]. A DPI lists a
  patient's ECGs and opens a trace without leaving its own screen and without an
  integration written for this hub in particular.
- The transactions live on their own mutually-authenticated listener, never on
  the API. They carry no authentication of their own — the profile expects it
  from a grouped ATNA actor — so mTLS is the whole security model, and every
  part of it is refused at startup rather than defaulted.
- CARD-6 refuses a document that would carry no patient identity: §4.6.4.2.2.1
  does not allow anonymous ECG documents on that transaction.
- `tools/ihe-display.html` is a Display actor to test and demonstrate against,
  and `scripts/gen-ihe-certs.sh` issues a throwaway PKI for it.

- **Patient Update [RAD-12]** as a receiver: an inbound MLLP listener applies
  `ADT^A08` demographic updates and `ADT^A40` merges, using the same field
  mappings the query path uses. Off by default — it is a write path into patient
  identity reached over the network. See
  [`docs/HL7_INBOUND_ADT.md`](docs/HL7_INBOUND_ADT.md).
- An omitted field keeps its stored value and a field sent as `""` is removed,
  as the profile requires. A replayed message older than the last one applied is
  ignored, which is what stops a retransmission putting a three-week-old name
  back.
- Admin > HL7 lists what arrived and what became of it, refusals included. No
  message body is stored.

### Patient identity

- The HIS may answer a query with a different identifier than the one it was
  asked about, so a site can let a device record the number scanned off a
  wristband and have it resolved to the establishment's own identifier. The
  patient is moved there, ECGs included.
- MAC-address whitelist for the FTP, DICOM and ECTP ports, with manual enrolment
  and the ability to refuse what cannot be identified. It only works where the
  device is on the same layer-2 segment, which the deployment now declares
  explicitly rather than leaving to chance.

### Distribution

- A DICOM proxy connector converts what it is given. A vendor file accepted by
  the extension filter used to be handed to the C-STORE association raw and
  refused by the PACS — a failure reported by the wrong side of the link.
- A connector can wait for HL7 enrichment before forwarding, so the document
  carries the establishment's demographics rather than what the device recorded.
  The wait ends on any terminal enrichment state: a HIS that never answers
  delays a delivery, it does not cancel it.

### Fixed

- A changed HL7 host took effect only after a restart. The settings screen built
  its own client for the test query, so the button reached the new address while
  enrichment, the retry job and the scheduler kept using the old one — silently,
  and with no error to show for it.
- A Connect procedure with no permission mapping was served unchecked to every
  authenticated caller. It is refused now, and a test catches the omission
  earlier still.
- Conversion tools come from ecg-bridge v1.4.0, which the hub already expected:
  `fda-to-pdf` now knows `-identity-unverified`, so a PDF for a patient the HIS
  had not confirmed no longer fails outright. The renderer also gained the
  calibration pulse and the acquisition date, and an injected date of birth
  reaches the document.
- An ECG ingested as FDA aECG XML could not be rendered as PDF at all.
- A date bound sent without a timezone in a CARD-5 query was read as UTC rather
  than the site's wall clock, shifting the window far enough to hide the ECG
  being looked for.

## [1.0.0] — 2026-09-08

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
- `ingest.max_file_bytes` (default 1Mi) caps an incoming file at every
  boundary. Every stage of the pipeline holds the file in memory, and the FTP
  and DICOM ports are published to devices, so an unbounded upload was enough
  to push the backend into the OOM killer.
- FTP authentication failures are counted per source address: the first three
  are answered at full speed, then a second of delay per extra failure, capped
  at 30s and forgotten after 15 minutes of silence or a successful login.
  `ftp_auth_failures_total` exports the count. No lockout on purpose — behind
  Docker's port mapping every device shares the gateway address, and a lockout
  would let a scanner take clinical ingestion offline.
- File names built out of sender-controlled input — DICOM `PatientID`,
  `StudyDate`/`StudyTime`, the SOP Instance UID, and the patient identifier
  parsed out of a vendor file on the FTP side — are reduced to a single safe
  path component before they reach the pipeline. The storage and quarantine
  writers additionally resolve the join and refuse a result that leaves its
  base directory.
- HL7 `QRY^A19` fields are escaped, as the ORU builder already did. Segment
  injection was already refused before dialling, but `^`, `~`, `&` and `\`
  passed through, so an identifier carrying `^` split QRD-8 into components and
  the HIS read a different query than the one intended.

### Operations

- Bridge networking with published device ports; FTP published on the host as
  `${FTP_PORT:-21}` while the container binds 2121 unprivileged.
- Passive port range 30000-30100, sized so concurrency is not the ceiling: the
  previous eleven-port range began refusing transfers at ten in parallel.
- Around 40 Prometheus metrics and a Grafana dashboard.
- `loadtest/capacity-probe.sh` reports the CPU cost of a single ECG, measured
  at ~27 ms on a 2-core deployment.

### Fixed before release

- Selecting a patient cleared the unviewed badge in the UI but marked nothing:
  `ecgs.patient_id` holds the device string while the UI sends the UUID, so the
  update matched no rows. Both identifier forms now resolve.
- The FTP passive range disagreed between the compose file, the module defaults
  and the deployment guide. The module bound ports Docker did not publish, and
  passive transfers were refused right after the `227` reply.
- The ECG viewer zoomed on a trackpad pinch but not on a mouse wheel: the wheel
  handler required ctrl/cmd, which only a pinch sets, so a wheel fell through to
  a pan branch that does nothing until the view is already zoomed.
- An expired session left the UI in its authenticated state until a manual
  reload, since authentication was checked once on mount. The app now returns to
  the login screen when the server rejects a call, and clears the query cache
  with it.
- The pagination footer disappeared when a patient was selected, and the bulk
  action bar covered it.
- The tag dropdown rendered behind the statistics cards: a leftover GSAP
  transform created a stacking context the dropdown could not escape.
- The session-expiry handler parked the app on its loading screen instead of
  the login form: clearing the query cache on every event made the mounted
  queries refetch, the authenticated ones answered 401, and the interceptor
  fired the event again. The listener is now registered only while the session
  is authenticated, so it runs exactly once.
- The GE MUSE probe matched `<RestingECG>` in any namespace, since its
  `XMLName` tag carried none. MUSE roots are unqualified, so a namespaced root
  is now rejected explicitly rather than letting the root name alone
  discriminate between vendors.

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
