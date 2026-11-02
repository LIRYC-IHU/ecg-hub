# DICOM Conformance Statement — ECG Hub

| | |
|---|---|
| **Product** | ECG Hub |
| **Product version** | 1.1.0 |
| **Conversion toolkit** | ecg-bridge v1.4.0 |
| **Manufacturer** | IHU LIRYC |
| **Document date** | 2026-10-25 |
| **Document revision** | 1 |
| **Repository** | https://github.com/LIRYC-IHU/ecg-hub |

Structured after DICOM PS3.2 Annex A. Applies to the version above only: the
behaviour described here is read from the source code of that version, and a
later version may differ.

---

## 1. Conformance Statement Overview

ECG Hub collects electrocardiograms from acquisition devices and serves them to
clinicians. On the DICOM side it does three things:

- **Receives** waveform objects pushed by modalities (Storage SCP).
- **Forwards** ECGs to a remote archive (Storage SCU). Objects that are not
  already DICOM are first converted into a 12-Lead ECG Waveform object.
- **Answers and sends** verification requests (Verification SCP and SCU).

It offers no query, retrieve, worklist, procedure step or storage commitment
service, and no media interchange.

### 1.1 Network services

| SOP Class | SOP Class UID | User of Service (SCU) | Provider of Service (SCP) |
|---|---|---|---|
| **Transfer** | | | |
| 12-Lead ECG Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.1.1 | Yes | Yes |
| General ECG Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.1.2 | Yes | Yes |
| Ambulatory ECG Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.1.3 | Yes | Yes |
| Hemodynamic Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.2.1 | Yes | Yes |
| Basic Cardiac Electrophysiology Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.3.1 | Yes | Yes |
| Arterial Pulse Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.5.1 | Yes | Yes |
| Respiratory Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.6.1 | Yes | Yes |
| General Audio Waveform Storage | 1.2.840.10008.5.1.4.1.1.9.4.1 | Yes | Yes |
| **Verification** | | | |
| Verification | 1.2.840.10008.1.1 | Yes | Yes (configurable) |
| **Query/Retrieve, Workflow Management, Print** | | No | No |

Only **12-Lead ECG Waveform Storage** is fully processed: parsed for display,
converted, and the only class ECG Hub creates. Instances of the other waveform
classes are received, stored and forwarded unchanged, but the viewer and the
converters do not read them. See §4.2.1.3.3 for how the SCP handles SOP classes
outside this table.

### 1.2 Media services

None. ECG Hub does not read or write DICOM media (PS3.10 file-sets). Single
DICOM files can be downloaded from the web interface. They are plain PS3.10
files with no DICOMDIR and no media application profile.

---

## 2. Table of Contents

1. Conformance Statement Overview
2. Table of Contents
3. Introduction
4. Networking
5. Media Interchange
6. Support of Character Sets
7. Security
8. Annexes
   - A. IOD Contents of Created Instances
   - B. Attribute Coercion and Data Flow
   - C. Coded Terminology
   - D. Known Deviations and Limitations

---

## 3. Introduction

### 3.1 Revision history

| Revision | Date | Product version | Changes |
|---|---|---|---|
| 1 | 2026-10-25 | 1.1.0 | First issue |

### 3.2 Audience

PACS administrators, integration engineers and biomedical engineering teams who
connect ECG Hub to modalities or to an archive. A working knowledge of DICOM
PS3.2, PS3.4 and PS3.7 is assumed.

### 3.3 Remarks

- This document states what the software does. It does not certify that
  interoperation with a given system works. Validate every connection on site
  before clinical use.
- **Annex D lists known deviations from the standard.** Read it before
  connecting an archive: several items there change what an archive receives.
- ECG Hub is not CE-marked and claims no medical device status. This statement
  is an interoperability document and carries no regulatory claim.

### 3.4 Terms and definitions

| Term | Meaning |
|---|---|
| AE | Application Entity |
| SCP / SCU | Service Class Provider / User |
| PDU | Protocol Data Unit |
| IOD | Information Object Definition |
| HIS | Hospital Information System (French: SIH) |
| Connector | An outbound destination configured in ECG Hub (here: a DICOM archive) |
| Quarantine | ECG Hub's holding area for files it could not ingest or could not attach to a patient |
| Device whitelist | ECG Hub's list of hardware allowed to send, keyed on MAC address |

### 3.5 References

- DICOM PS3.2, PS3.3, PS3.4, PS3.5, PS3.6, PS3.7, PS3.8, PS3.15 (current
  edition at the date of this document).
- [`docs/DAT.md`](DAT.md) — technical architecture dossier (French), for
  network flows, deployment and security.
- [`docs/IHE_INTEGRATION_STATEMENT.md`](IHE_INTEGRATION_STATEMENT.md).

---

## 4. Networking

### 4.1 Implementation model

#### 4.1.1 Application data flow

```
                                  ┌───────────────────────────────┐
  Modality ── C-STORE / C-ECHO ──▶│ STORE-SCP AE                  │
                                  │   ↓ ingestion queue           │
                                  │   ↓ parse, patient matching,  │
                                  │     storage, HL7 enrichment   │
                                  │   ↓                           │
                                  │ STORE-SCU AE (one per         │── C-STORE / C-ECHO ──▶ Archive
                                  │   configured connector)       │
                                  └───────────────────────────────┘
```

- **Receive ECG from modality.** A modality opens an association with the
  STORE-SCP AE and sends instances. Each one goes into the ingestion queue and is
  then parsed, attached to a patient and stored.
- **Forward ECG to archive.** Once an ECG is stored, each enabled connector whose
  filters match it forwards it. If the connector is set to wait for HL7, the
  forward is held until enrichment from the HIS has finished. Files that land in
  quarantine are forwarded too (Annex B.3).
- **Verify archive.** The STORE-SCU AE sends a C-ECHO each time connector health
  is checked: the status page, the health endpoint, or a connection test from the
  admin interface.

#### 4.1.2 Functional definitions of AEs

**STORE-SCP.** Listens on one TCP port, optionally over TLS. Accepts
associations from devices on the device whitelist, answers C-ECHO, and receives
C-STORE. Each received instance is rebuilt as a PS3.10 file and placed in the
ingestion queue.

**STORE-SCU.** One instance per configured DICOM connector. For each object to
send, it opens one association, sends one C-STORE and releases the association.
Objects that are not DICOM are converted into a 12-Lead ECG Waveform instance
first (Annex A).

#### 4.1.3 Sequencing of real-world activities

Forwarding of an ECG starts only after it has been stored locally. If its
connector waits for HL7, forwarding starts only after HL7 enrichment has
finished (successfully or not). A HIS that never answers delays the forward but
does not cancel it.

### 4.2 AE specifications

#### 4.2.1 STORE-SCP

##### 4.2.1.1 SOP classes

The SOP classes of §1.1 in the SCP role. Verification is answered only when
"C-ECHO" is enabled in the DICOM module settings (on by default).

##### 4.2.1.2 Association policies

**General.**

| | |
|---|---|
| Application Context Name | 1.2.840.10008.3.1.1.1 |
| Maximum PDU size offered | 4,194,304 bytes |

**Number of associations.** Unlimited. Each accepted connection is served
independently.

**Asynchronous nature.** Not supported. No asynchronous operations window is
negotiated.

**Implementation identifying information.** The A-ASSOCIATE-AC sent by this AE
carries **no** Implementation Class UID and no Implementation Version Name
(Annex D, D-3).

##### 4.2.1.3 Association acceptance policy

###### 4.2.1.3.1 Who may associate

Acceptance is decided by the **device whitelist**, not by AE titles:

- When the TCP connection arrives, ECG Hub resolves the MAC address behind the
  source IP. An approved device continues. A device that is unknown or pending
  pairing continues in pairing mode: its objects are held for an operator. A
  refused device has its **TCP connection closed before any association
  negotiation**, so the modality sees a dropped connection, not an
  A-ASSOCIATE-RJ.
- The whitelist is checked again before each C-STORE in the association, so a
  device revoked mid-association gets status `0124` from then on.
- Calling and Called AE Titles are **not checked** (Annex D, D-4).
- MAC resolution requires the device to be on the same network segment as the
  server. Behind NAT or routing the whitelist cannot work (see `DAT.md` §3.2).

No A-ASSOCIATE-RJ is ever sent by the application.

###### 4.2.1.3.2 Real-world activity: receive ECG from modality

**Presentation context acceptance.** The SCP **accepts every proposed
presentation context**. In each one it selects **the first transfer syntax the
requester lists**. It does not check either the abstract syntax or the transfer
syntax against a supported list (Annex D, D-1).

The transfer syntaxes ECG Hub can actually parse are:

| Transfer Syntax | UID |
|---|---|
| Implicit VR Little Endian | 1.2.840.10008.1.2 |
| Explicit VR Little Endian | 1.2.840.10008.1.2.1 |
| Explicit VR Big Endian (retired) | 1.2.840.10008.1.2.2 |

**Modalities should be configured to propose only these, and only the SOP
classes of §1.1.**

| Abstract Syntax | Transfer Syntax | Role | Extended Negotiation |
|---|---|---|---|
| Any SOP class of §1.1 | Any of the three above | SCP | None |
| Verification | Implicit VR Little Endian | SCP | None |

###### 4.2.1.3.3 SOP-specific conformance — Storage SCP

**Level of support.** Level 2 (Full). The received data set is kept as
received, byte for byte, wrapped in a newly written File Meta Information
header. Private and Type 3 attributes are retained. Exceptions are listed in
Annex B.2.

**Meaning of Success.** `0000` means the instance **has been placed in the
ingestion queue**. Parsing, patient matching and persistence happen afterwards,
outside the association. A failure at that stage sends the file to quarantine,
where it is kept and visible to operators, but **the modality is not told**
(Annex D, D-2).

**SOP classes outside §1.1.** Accepted at association level (see above). Any
object with a valid DICM preamble is processed as an ECG, so a non-waveform
instance is stored and shows up as an ECG that cannot be displayed. Do not
route other modalities to this AE.

**Duplicate SOP Instance UIDs.** Not detected at DICOM level. A resent instance
is ingested again as a separate ECG record, subject to the application's own
duplicate handling.

**Size limit.** Instances larger than the configured ingestion limit (default
1 MiB) are refused with `A700`. A truncated copy is kept in quarantine with the
reason, so the refusal can be audited.

**Status codes returned.**

| Status | Meaning | When |
|---|---|---|
| `0000` | Success | Queued for ingestion |
| `A700` | Refused: out of resources | Ingestion queue full (transient: resend later), or instance over the size limit (permanent) |
| `0124` | Refused: not authorized | Device revoked during the association, or data set could not be rebuilt into a file |

###### 4.2.1.3.4 SOP-specific conformance — Verification SCP

Returns `0000` when C-ECHO is enabled and `0211` (unrecognized operation) when
it is disabled.

###### 4.2.1.3.5 Other DIMSE services

C-FIND, C-MOVE and C-GET requests are answered with `0211` (unrecognized
operation). N-services are not supported.

#### 4.2.2 STORE-SCU

##### 4.2.2.1 SOP classes

The SOP classes of §1.1 in the SCU role, plus Verification.

##### 4.2.2.2 Association policies

| | |
|---|---|
| Application Context Name | 1.2.840.10008.3.1.1.1 |
| Maximum PDU size offered | 4,194,304 bytes (C-STORE); 16,384 bytes (C-ECHO) |
| Number of associations | One per object being forwarded. Forwards to several connectors, and of several ECGs, run concurrently |
| Asynchronous nature | Not supported |
| Implementation Class UID (C-STORE associations) | `1.2.826.0.1.3680043.9.7133.1.1` (Annex D, D-3) |
| Implementation Version Name (C-STORE associations) | `GODICOM_1_1` |
| Implementation identifying information (C-ECHO associations) | None sent (Annex D, D-3) |

##### 4.2.2.3 Association initiation policy

###### 4.2.2.3.1 Real-world activity: forward ECG to archive

The SCU proposes one presentation context per abstract syntax: Verification,
the SOP class of the object being sent, and every SOP class of §1.1, with
duplicates removed. Each context offers the same four transfer syntaxes:

| Abstract Syntax | Transfer Syntaxes proposed | Role | Extended Negotiation |
|---|---|---|---|
| Verification, the object's SOP class, all SOP classes of §1.1 | Implicit VR Little Endian 1.2.840.10008.1.2; Explicit VR Little Endian 1.2.840.10008.1.2.1; Explicit VR Big Endian 1.2.840.10008.1.2.2; Deflated Explicit VR Little Endian 1.2.840.10008.1.2.1.99 | SCU | None |

The data set is re-encoded in the transfer syntax the archive accepts, with
one exception: Deflated Explicit VR Little Endian is proposed but cannot be
produced (Annex D, D-15). **Configure the archive to accept one of the first
three.**

**Objects of a SOP class outside §1.1** are still sent, on a context proposed
for their own class, and a warning is logged. The connector model carries a
`StrictSOP` field that would refuse them instead, but it cannot be set
(Annex D, D-16).

**Which file is sent.**

- An ECG received as DICOM is sent **as stored**, which is essentially as the
  modality sent it (Annex B.2).
- Any other ECG (Philips, GE MUSE, Nihon Kohden, Mindray, FDA aECG) is converted
  to a 12-Lead ECG Waveform instance first. If the patient is known locally, the
  establishment's demographics replace the device's in that instance (Annex A,
  Annex B.1).

**Association release.** After one C-STORE the SCU sends A-RELEASE-RQ. If the
send times out (default 30 s, configurable per connector), the association is
released.

###### 4.2.2.3.2 SOP-specific conformance — Storage SCU

| Status received | Behaviour |
|---|---|
| `0000` Success | Forward recorded as delivered |
| Any other status, **warnings (`B000`, `B006`, `B007`) included** | Treated as a failure and retried (Annex D, D-6) |
| Association rejected, aborted, or timeout | Treated as a failure and retried |

Retries follow the connector's retry policy (maximum attempts and interval,
configurable). Once the attempts run out, the forward is marked as exhausted
and shown in the admin interface. Each attempt is written to the audit log.

###### 4.2.2.3.3 Real-world activity: verify archive

The SCU sends A-ASSOCIATE-RQ with a single presentation context: Verification,
Implicit VR Little Endian. It then sends one C-ECHO and checks for status
`0000`.

##### 4.2.2.4 Association acceptance policy

STORE-SCU accepts no associations.

### 4.3 Network interfaces

| | |
|---|---|
| Physical network | Any interface supported by the host. ECG Hub runs as a container |
| Protocol | TCP/IP (PS3.8). IPv4 and IPv6, as configured on the host |
| TLS | STORE-SCP: optional (§7). STORE-SCU: **not supported** (Annex D, D-5) |

### 4.4 Configuration

Everything except the TLS certificate is set from the admin interface
(Modules > DICOM, and Connectors) and stored in the database.

#### 4.4.1 STORE-SCP

| Parameter | Default |
|---|---|
| Enabled | Off until enabled |
| AE Title | `ECG-HUB` |
| Listening port (in the container) | `4242`. Published on the host as `DICOM_PORT`, default `4242` |
| C-ECHO enabled | Yes |
| TLS | Off |
| Maximum object size | 1 MiB (`ingest.max_file_bytes` in `config.yaml`) |
| Device whitelist | See the admin interface (Devices) |

#### 4.4.2 STORE-SCU (per connector)

| Parameter | Default |
|---|---|
| Remote host and port | None. Usual ports are 104 and 11112 |
| Calling AE Title | `ECGHUB` |
| Called AE Title | `ANY-SCP` |
| Timeout | 30 s |
| Filters | File extension and vendor. Empty means everything |
| Wait for HL7 enrichment | Off |
| Retry policy | Maximum attempts and interval |

The archive must be configured to accept the Calling AE Title above from the
ECG Hub host's address.

---

## 5. Media Interchange

Not supported.

---

## 6. Support of Character Sets

**Received instances.** Stored and forwarded byte for byte. Specific Character
Set (0008,0005) is neither interpreted nor rewritten. The patient ID, name and
dates that ECG Hub extracts for its own database are read by the DICOM library.
Only Patient ID and study and acquisition dates drive ingestion.

**Created instances.** Specific Character Set is set to `ISO_IR 100`
(Latin-1). However, the text values are currently **written as UTF-8 bytes,
without transcoding to Latin-1**. Names with accented characters are therefore
misread by a receiver that honours the declared character set (Annex D, D-11).
Plain ASCII values are unaffected.

---

## 7. Security

### 7.1 Security profiles

No PS3.15 security profile is claimed.

### 7.2 Association-level security

- **STORE-SCP over TLS** (optional). The server presents one certificate per
  installation, mounted from the host (see [`TLS_CERTS.md`](TLS_CERTS.md)). The
  minimum version is TLS 1.2. **The server does not request a client
  certificate**, so the modality is not authenticated by TLS. When TLS is
  enabled, the port accepts only TLS.
- **Access control** relies on the MAC-address device whitelist (§4.2.1.3.1),
  not on AE titles or certificates.
- **STORE-SCU** traffic is unencrypted (Annex D, D-5). Protect it at network
  level.

### 7.3 Application-level security

User access, roles and the audit trail are handled by the application. They
are described in `DAT.md` §7. DICOM receptions, refusals and forwards are
counted in Prometheus metrics and logged.

---

## 8. Annexes

### Annex A — IOD Contents of Created Instances

ECG Hub creates DICOM instances only through ecg-bridge v1.4.0, when converting
a non-DICOM ECG. This happens when forwarding to an archive or when a user
exports to DICOM. Every created instance is a **12-Lead ECG Waveform Storage**
instance (1.2.840.10008.5.1.4.1.1.9.1.1), encoded in **Explicit VR Little
Endian**.

| Source format | Converter |
|---|---|
| Philips XML | `philips-to-dicom` |
| GE MUSE XML | `muse-to-dicom` |
| Nihon Kohden `.DAT` | `nk-to-dicom` |
| Mindray | `mindray-to-dicom` |
| FDA HL7 aECG XML | `fda-to-dicom` |

#### A.1 File Meta Information

| Attribute | Tag | Value |
|---|---|---|
| Media Storage SOP Class UID | (0002,0002) | 1.2.840.10008.5.1.4.1.1.9.1.1 |
| Media Storage SOP Instance UID | (0002,0003) | Same as (0008,0018) |
| Transfer Syntax UID | (0002,0010) | 1.2.840.10008.1.2.1 |
| Implementation Class UID | (0002,0012) | 2.25.83057759282917960894454627989 |
| Implementation Version Name | (0002,0013) | ECGHUB-CONV-1 |

#### A.2 Modules and attributes

The 12-Lead ECG IOD (PS3.3 A.34.3). Legend: ✓ written · — absent · **inj.** may
be replaced by HIS demographics (Annex B.1). **Bold** marks absences that break
IOD conformance (Annex D, D-10).

| Module | Attribute | Tag | Type | Philips | MUSE | NK | Mindray | FDA |
|---|---|---|---|---|---|---|---|---|
| Patient | Patient's Name | (0010,0010) | 2 | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. |
| | Patient ID | (0010,0020) | 2 | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. |
| | Patient's Birth Date | (0010,0030) | 2 | **—** | **—** | **—** | **—** | ✓ inj. |
| | Patient's Sex | (0010,0040) | 2 | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. | ✓ inj. |
| General Study | Study Instance UID | (0020,000D) | 1 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Study Date / Time | (0008,0020/0030) | 2 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Referring Physician's Name | (0008,0090) | 2 | **—** | **—** | **—** | **—** | **—** |
| | Study ID | (0020,0010) | 2 | ✓ | ✓ | **—** | ✓ | ✓ |
| | Accession Number | (0008,0050) | 2 | **—** | **—** | ✓ | **—** | **—** |
| Patient Study | Patient's Age | (0010,1010) | 3 | ✓ | ✓ | — | — | ✓ |
| General Series | Modality | (0008,0060) | 1 | ECG | ECG | ECG | ECG | ECG |
| | Series Instance UID | (0020,000E) | 1 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Series Number | (0020,0011) | 2 | ✓ | ✓ | ✓ | ✓ | ✓ |
| General Equipment | Manufacturer | (0008,0070) | 2 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Institution Name, Model Name, Software Versions | | 3 | ✓ when present in source | | | | |
| Waveform Identification | Instance Number | (0020,0013) | 1 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Content Date / Time | (0008,0023/0033) | 1 | ✓ | ✓ | **—** | ✓ | ✓ |
| | Acquisition DateTime | (0008,002A) | 1 | ✓ | ✓ | **—** | ✓ | ✓ |
| Waveform | Waveform Sequence | (5400,0100) | 1 | ✓ | ✓ | ✓ | ✓ | ✓ |
| Acquisition Context | Acquisition Context Sequence | (0040,0555) | 2 | **—** | **—** | **—** | **—** | **—** |
| Waveform Annotation | Waveform Annotation Sequence | (0040,B020) | 1C | ✓ | ✓ | ✓ | ✓ | ✓ |
| SOP Common | SOP Class UID / SOP Instance UID | (0008,0016/0018) | 1 | ✓ | ✓ | ✓ | ✓ | ✓ |
| | Specific Character Set | (0008,0005) | 1C | ISO_IR 100 | ISO_IR 100 | ISO_IR 100 | ISO_IR 100 | ISO_IR 100 |

The Synchronization module is not written. It is required only when time
synchronization was applied, which these sources do not report.

#### A.3 Waveform Sequence content

- Each lead is a channel with its source given in the SCPECG coding scheme
  (Annex C). Sensitivity is in µV (UCUM `uV`), and baseline, skew and filter
  settings are carried over from the source when it provides them.
- Waveform Data (5400,1010) is 16-bit signed (`SS`), VR `OW`.
- Some converters write two items: an `ORIGINAL` rhythm item and a `DERIVED`
  item for the median or representative beat, when the source has one.
- Device measurements from the source, when present, go in the Waveform
  Annotation Sequence. **ECG Hub adds no measurement or interpretation of its
  own.**
- Samples are copied from the source without resampling.

#### A.4 UID generation

| Converter | Study Instance UID | Series Instance UID | SOP Instance UID | Same UID on reconversion |
|---|---|---|---|---|
| Philips | `2.25.<time in ns>` | Study UID + `.1` | **= Study UID** | No |
| GE MUSE | Random `2.25` UUID | Random `2.25` UUID | Random `2.25` UUID | No |
| Nihon Kohden | Random `2.25` UUID | Random `2.25` UUID | Random `2.25` UUID | No |
| Mindray | `1.2.840.113619.2.755.<serial>.<acq. time>` | Study UID + `.1` | **= Study UID** | Yes |
| FDA aECG | Derived from the aECG document id | Study UID + `.1` | **= Study UID** | Yes |

The bold cells, the Philips UID form and the Mindray root are deviations
(Annex D, D-7 to D-9). Where reconversion does not keep the UIDs, the same ECG
forwarded or exported twice reaches the archive as **two distinct studies**.

### Annex B — Attribute Coercion and Data Flow

#### B.1 Created instances

If the patient is known locally (in practice, once HL7 enrichment has run), the
patient's ID, name, sex and birth date replace the device's values in the
converted instance. Patient's Age is then cleared rather than kept, because
the device's age could contradict the injected birth date. Any field ECG Hub
does not have keeps the device's value. Philips, MUSE and NK instances do not
carry the birth date even when it is injected (A.2).

#### B.2 Received instances

Received instances are **not coerced** on reception or on forwarding. In
particular:

- Demographics from HL7 enrichment are **not** written into received DICOM
  instances. Only converted instances get them (B.1).
- When an operator attaches an unidentified ECG to a patient, the patient is
  linked in the database. **The file is not rewritten.**
- Merges and demographic updates received from the HIS (ADT A08/A40) update
  the database only.
- The one exception: an operator **editing the Patient ID** of an ECG in the web
  interface rewrites Patient ID (0010,0020) in the stored file. No other
  attribute is changed, and the SOP Instance UID is kept. The edited file is
  **not** forwarded again.

An archive fed by ECG Hub therefore holds, for received DICOM, the
demographics **the modality** recorded. If patient identity has to be
reconciled in the archive, the archive's own patient update mechanism must do
it.

#### B.3 Unidentified and quarantined instances

A file that ECG Hub cannot attach to a patient goes to quarantine and is
**forwarded right away, unchanged**, so the archive is not deprived of it. If
an operator later attaches it to a patient, it is ingested again and forwarded
**a second time, still unchanged**. For received DICOM, the second send has the
same SOP Instance UID and the same (possibly empty) Patient ID.

### Annex C — Coded Terminology

| Use | Coding scheme | Values |
|---|---|---|
| Channel source (lead) | `SCPECG` | `5.6.3-9-1` (I), `-2` (II), `-3`…`-8` (V1–V6), `-61` (III), `-62` (aVR), `-63` (aVL), `-64` (aVF) |
| Channel sensitivity units | `UCUM` | `uV` |

No private attributes, private SOP classes or private transfer syntaxes are
created.

### Annex D — Known Deviations and Limitations

Numbered so that each can be tracked to closure. Stated as of version 1.1.0
with ecg-bridge v1.4.0.

| # | Area | Deviation | Effect | Where |
|---|---|---|---|---|
| D-1 | SCP negotiation | Every presentation context is accepted, whatever its abstract syntax, using the first transfer syntax proposed | A modality may send an unsupported SOP class or transfer syntax and get Success. The object then fails parsing (quarantine) or is stored as an ECG that cannot be displayed | `go-netdicom` provider; `internal/dicom/server.go` |
| D-2 | SCP status | Success is returned when the object is queued, before parsing and persistence | A later ingestion failure is not reported to the modality. The file is kept in quarantine | `internal/dicom/server.go` |
| D-3 | Implementation identification | The SCP's A-ASSOCIATE-AC and the SCU's C-ECHO A-ASSOCIATE-RQ carry no Implementation Class UID (mandatory under PS3.7 D.3.3.2). Where one is sent (SCU C-STORE, File Meta of received objects), it is the go-dicom library's UID, not one belonging to ECG Hub | Strict peers may reject the association. Logs on the peer do not identify ECG Hub | `go-netdicom`; `internal/connector/dicom/scu_client.go` |
| D-4 | SCP access | Called and Calling AE Titles are not checked | A modality configured with the wrong AE title still connects. Access control is the MAC whitelist only | `internal/dicom/server.go` |
| D-5 | SCU security | No TLS on outbound associations. The connector model has a `TLS` field, but it is neither exposed nor used | Forwards to the archive travel in clear. `DAT.md` §6.2 and §7.2 currently state the opposite | `internal/connector/dicom/scu_client.go` |
| D-6 | SCU status | Warning statuses are treated as failures and retried | An archive answering with a warning (e.g. `B000` coercion) gets the same object again until attempts run out | `go-netdicom` `cstore.go` |
| D-7 | UIDs | Philips, NK and MUSE conversions generate new UIDs on every run. The Philips form `2.25.<ns>` is not derived from a UUID as PS3.5 B.2 requires | Duplicate studies in the archive when the same ECG is forwarded twice (retry after a lost response, resend, export then forward) | ecg-bridge builders |
| D-8 | UIDs | Philips, Mindray and FDA conversions use the same UID for Study and SOP Instance | One UID identifies two different entities. Some archives refuse this or mix up the levels | ecg-bridge builders |
| D-9 | UIDs | The Mindray UID root `1.2.840.113619` is GE Healthcare's registered root | UIDs under a root ECG Hub does not own, with a risk of collision with genuine GE instances | `mindray-to-dicom/builder.go` |
| D-10 | IOD | Mandatory attributes are missing: Acquisition Context Sequence (all), Referring Physician's Name (all), Accession Number (all but NK), Patient's Birth Date (all but FDA), Study ID, Content Date/Time and Acquisition DateTime (NK) | Created instances do not conform to the 12-Lead ECG IOD. Validators (e.g. dciodvfy) report them, and strict archives may refuse them | ecg-bridge builders |
| D-11 | Character set | `ISO_IR 100` is declared but values are written in UTF-8 | Accented names are garbled at the receiver | `dicomconf/finalize.go` and builders |
| D-12 | Coercion | Patient identity resolved in ECG Hub (HL7, operator assignment, ADT) is not propagated into received DICOM instances, and not re-forwarded (Annex B.2, B.3) | The archive keeps the modality's demographics, and unidentified objects arrive twice | `internal/connector`, `internal/api/handlers` |
| D-13 | SOP classes | Only 12-Lead ECG is parsed. The other waveform classes are stored and forwarded only | No display or conversion for General, Ambulatory, Hemodynamic and similar instances | `internal/ecgwaveform` |
| D-14 | SCP TLS | Server authentication only. No client certificate requested | TLS does not authenticate the modality | `internal/dicom/server.go` |
| D-15 | SCU negotiation | Deflated Explicit VR Little Endian (1.2.840.10008.1.2.1.99) is among the transfer syntaxes proposed, but the library cannot write it: `ParseTransferSyntaxUID` falls through Deflated to Explicit VR Little Endian, so the data set is encoded **uncompressed** under a syntax that announces compression | An archive that selects the Deflated context receives a data set it cannot decompress, and every forward to it fails. Implicit VR Little Endian is proposed first, so an archive that accepts it is unaffected | `go-dicom` `dicomio/transfersyntax.go`; `go-netdicom` `cstore.go` |
| D-16 | SCU configuration | `connector.DICOMEndpoint` carries a `StrictSOP` field, meant to refuse a SOP class outside §1.1 rather than forward it best-effort, but nothing ever sets it. Same defect as D-5, on a different field | Strict mode cannot be turned on. The permissive behaviour of §4.2.2.3.1 is the only one reachable | `internal/connector/config.go`; `cmd/ecg-hub/main.go` |
