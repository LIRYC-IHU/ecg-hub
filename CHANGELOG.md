# Changelog

All notable changes to this project are documented here. Versions follow
[semantic versioning](https://semver.org/).

## [Unreleased]

### Changed

- Without `DEVICE_WHITELIST=true` the `device.read` and `device.manage`
  permissions are hidden. No role grants them, so the Devices menu and its API
  are gone, and Admin > Roles no longer offers them. They stay stored on the
  roles, and saving a role keeps them: setting `DEVICE_WHITELIST=true` again
  brings them back as they were.

### Added

- The ECG viewer says why it cannot draw a waveform. An Ambulatory recording,
  a partial General ECG or a hemodynamic, respiratory or audio object is stored
  and forwarded like any other, but the viewer is built for the 12 standard
  leads in millivolts, and such files used to end in a generic "Failed to parse
  ECG waveform". The viewer now names the SOP class and the reason — the
  standard leads are absent, or a channel is not a voltage — and states that
  the file was stored and forwarded. A malformed file still reports a parse
  error (Annex D, D-13, narrowed).

- The DICOM SCP can check the Called AE Title (Modules > DICOM > Check Called
  AE, off by default). The DICOM library never looks at it — it echoes whatever
  title was asked for in its acceptance — so a modality configured for another
  archive was accepted and stored silently. With the option on, ECG Hub reads
  the association request before handing the connection to the library and
  refuses a foreign title with A-ASSOCIATE-RJ reason 7, which the modality logs
  as "called AE title not recognized". The Calling AE Title stays unchecked:
  the device whitelist is the access control (Annex D, D-4).
- The DICOM SCP can require mutual TLS (Modules > DICOM > Mutual TLS, off by
  default, needs TLS). Modalities must then present a client certificate
  signed by the CA bundle in `certs.client_ca_file` (`TLS_CLIENT_CA_FILE`);
  without one, or with one from another CA, the handshake fails and the
  connection is closed before any association, with the reason in the log.
  TLS used to authenticate the server only (Annex D, D-14). The SCP refuses to
  start, and the setting refuses to save, without TLS or a readable bundle.
- Connectors can let ECG Hub manage patient identity (Modules > Connectors >
  "ECG Hub manages patient identity", off by default). Without it, a connector
  stays a plain proxy: an unidentified ECG is forwarded from quarantine, then
  forwarded again after an operator assigns it, with the same SOP Instance UID
  and still no Patient ID, and received DICOM always carries the modality's
  demographics. With it, an unidentified ECG waits in quarantine and goes out
  once, after assignment, and received DICOM is sent with the Patient ID, name,
  birth date and sex ECG Hub holds (a coercion of the outbound copy only; the
  stored file is untouched). A coercion that fails fails the forward rather than
  send the modality's identity. Later ADT changes are still not re-forwarded
  (Annex D, D-12, narrowed).

### Changed

- ecg-bridge, the conversion toolkit, is pinned to v1.5.0 (was v1.4.0). That
  release carries its DICOM conformance fixes: deterministic UIDs, missing
  12-Lead ECG IOD attributes, and UTF-8 character set handling (the converter
  items of Annex D of the conformance statement).

### Fixed

- The wrong-patient check now works for DICOM files. Assigning an unidentified
  ECG compares the file's name, birth date and sex with the chosen patient,
  warns the operator and records the disagreement in the audit log — but the
  DICOM module extracted none of the three, so for files received over DICOM
  the check never fired and the preview showed "—". It now reads Patient's
  Name, Birth Date and Sex under the same keys as the other modules (#133).
- The Quarantine badge in the sidebar updates as soon as a file is assigned or
  deleted. It counts from the admin statistics, which those actions did not
  refresh, so it kept showing files that were already gone (#133).
- A C-STORE answered with a warning no longer counts as a failed forward. The
  DICOM library turns every non-zero status into an error, so `B000` (coercion
  of data elements), `B006` (elements discarded) and `B007` sent the same object
  back to an archive that had already stored it, once per retry, until the
  attempts ran out — filing a duplicate each time. A warning status is now read
  back out of the library's error, the forward is recorded as delivered, and the
  status is logged and counted separately: a coercion is the archive rewriting
  what was sent, and for `B000` that can be the patient identity (Annex D, D-6).
- ECG Hub identifies itself on the wire and in the files it writes. It had no
  Implementation Class UID of its own, so where one went out at all it was the
  DICOM library's `1.2.826.0.1.3680043.9.7133.1.1` / `GODICOM_1_1`: every object
  the hub stored named the toolkit as its producer, and a receiver could not key
  behaviour on a known sender. ECG Hub now has its own pair,
  `2.25.64792898169045814434475395748197887603` / `ECGHUB-1`, derived from a
  UUID as PS3.5 B.2 requires and fixed for good. It goes into the File Meta of
  every stored object and into the C-ECHO association request, which carried no
  implementation item at all although PS3.7 D.3.3.2 makes it mandatory. The
  C-STORE request and the SCP's association acceptance are built inside the
  library and still carry its identity (Annex D, D-3). ecg-bridge keeps its own
  pair: it is a separate implementation.
- The C-STORE SCP parses an object before answering Success. It answered as
  soon as the instance was queued, so a data set that would not parse was
  accepted, landed in quarantine, and the modality never learned that the
  transfer it considered successful had produced nothing usable. The parse now
  happens inside the association and a failure is refused with `C000` and the
  reason. Patient matching and persistence still follow afterwards, so Annex D
  D-2 is narrowed rather than removed. An object with no Patient ID is still
  accepted and routed to the unidentified review queue, as designed.
- Received DICOM files are named after the patient again. The name was built
  from the body the C-STORE callback receives, which carries no preamble, no
  DICM and no File Meta — so the parser could not know its transfer syntax and
  failed on every object, silently falling back to the SOP Instance UID. Every
  file received over DICOM since the SCP shipped has that fallback name. The
  name is now built from the reconstructed file, in the same parse the check
  above performs.
- The C-STORE SCP refuses what it cannot handle instead of storing it. The
  DICOM library accepts every presentation context it is offered — any abstract
  syntax, and whichever transfer syntax the caller proposed first — so a
  modality could send a CT image or a JPEG 2000 data set, read Success, and
  have the object end in quarantine or sit stored as an ECG nothing can draw.
  An object whose SOP class is outside the supported list is now answered
  `0112`, one whose transfer syntax is outside the three declared ones `C000`,
  each naming the offending UID. The contexts are still accepted: refusing them
  at negotiation needs a patched or replaced library (Annex D, D-1).
- A C-STORE that could not be reconstructed answered `0124` (Not authorized),
  sending whoever investigated to look at the device whitelist for what was
  really an unreadable object. It answers `C000` (Cannot understand) now.
- The supported SOP classes and transfer syntaxes moved to `internal/dicomsop`,
  shared by the SCP and the SCU. They were the connector's alone, and the
  server had no list at all to check against.
- The connector panel's **Test** button now talks DICOM. It opened a TCP socket
  and closed it, so it reported success for any port with something listening:
  an archive expecting TLS where the connector sent none, a certificate the host
  does not trust, a wrong Called AE title, or a port with no SCP behind it at
  all. A DICOM connector is now tested with a real association and C-ECHO, using
  that connector's own settings, so what the button reports is what a forward
  would do. ECTP and FTP connectors keep the reachability check.
- Outbound DICOM forwards can now be encrypted. `connector.DICOMEndpoint` had
  carried a `TLS` field since the connectors shipped, but nothing stored it,
  nothing showed it and the SCU never read it, so every C-STORE and C-ECHO to a
  PACS went out in clear whatever the intent — and `DAT.md` §6.2 and §7.2
  already described it as optional, which it was not. TLS is wired end to end
  now, off by default because DICOM has no in-band upgrade and an archive that
  does not expect it rejects the handshake. The archive's certificate is
  verified against the host's trust store, with a per-connector opt-out for an
  archive whose private CA cannot be installed there (Annex D, D-5).
- `StrictSOP`, which refuses an object whose SOP class is outside the supported
  list instead of forwarding it best-effort, was dead in the same way and is
  wired through the same path. The sending device is now told its object was
  rejected rather than having it quietly accepted (Annex D, D-16).
- Outbound C-STORE proposed Deflated Explicit VR Little Endian, a syntax the
  DICOM library announces but cannot write: it falls through to Explicit VR
  Little Endian when encoding, so an archive selecting that context would have
  received an uncompressed data set under a header claiming compression, and
  every forward to it would have failed. The SCU now proposes only the three
  syntaxes it can produce. Implicit VR Little Endian was proposed first, which
  is why no deployment hit this (Conformance Statement Annex D, D-15).

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
