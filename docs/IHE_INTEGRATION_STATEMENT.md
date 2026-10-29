# IHE Integration Statement — ECG Hub

| | |
|---|---|
| **Vendor** | IHU LIRYC |
| **Product name** | ECG Hub |
| **Product version** | 1.1.0 |
| **Date** | 2026-10-25 |
| **Repository** | https://github.com/LIRYC-IHU/ecg-hub |

This statement follows the form of IHE Technical Frameworks, General
Introduction, Appendix F. It covers the version above only.

> **Status.** This statement has not been validated at an IHE Connectathon. The
> implementation follows the published framework text and has been tested
> against the *Display* actor shipped with this repository
> (`tools/ihe-display.html`). Validating it at a Connectathon would make the
> statement verifiable by a third party.

---

## 1. Integration profiles implemented

| IHE Domain | Integration Profile | Actor | Options |
|---|---|---|---|
| Cardiology (CARD) | Retrieve ECG for Display (ECG) | Information Source | None defined. The `SUMMARY` and `SUMMARY-CARDIOLOGY` request types of ITI-11, which the profile requires of an Information Source supporting CARD-5, are implemented |

No other actor is claimed. In particular, the support for **Patient Update
[RAD-12]** described in §3 is a transaction implemented in the receiving role.
It is **not** a claim to the Order Placer or Department System
Scheduler/Order Filler actors of Scheduled Workflow, which require
transactions ECG Hub does not implement.

## 2. Related documents

- DICOM: [`DICOM_CONFORMANCE_STATEMENT.md`](DICOM_CONFORMANCE_STATEMENT.md)
- HL7 inbound ADT: [`HL7_INBOUND_ADT.md`](HL7_INBOUND_ADT.md)
- Architecture, flows and security (French): [`DAT.md`](DAT.md), §6, §7 and §13

Frameworks referenced:

- IHE Cardiology Technical Framework, Vol. 1 and 2, Revision 4.0 (Final Text)
  — Retrieve ECG for Display, CARD-5 (§4.5), CARD-6 (§4.6)
- IHE IT Infrastructure Technical Framework, Vol. 2 — ITI-11 (§3.11)
- IHE Radiology Technical Framework, Vol. 2 — RAD-12 (§4.12)

---

## 3. Transaction details

### 3.1 Retrieve ECG for Display — Information Source

| Transaction | Role | Supported | Notes |
|---|---|---|---|
| Retrieve ECG List [CARD-5] | Responder | Yes | `requestType=SUMMARY-CARDIOLOGY-ECG`. XML per CARD TF-2 Appendix C, linked to a server-side stylesheet |
| Retrieve Specific Information for Display [ITI-11] | Responder | Partially | `SUMMARY` and `SUMMARY-CARDIOLOGY` only, both in XHTML. No other ITI-11 request type is answered (404) |
| Retrieve ECG Document for Display [CARD-6] | Responder | Yes | `application/pdf` only |

**Binding.** HTTP GET only, at the implementation-independent locations
`/IHERetrieveSummaryInfo` and `/IHERetrieveDocument`. A WSDL describing the GET
binding is served at `/ihe/retrieve-for-display.wsdl`. The SOAP binding is not
supported.

**CARD-5 / ITI-11 request parameters.**

| Parameter | Supported | Behaviour |
|---|---|---|
| `requestType` | Yes | Values as above. Any other value returns 404 |
| `patientID` | Yes | HL7 CX. When `ihe.assigning_authority` is set, the assigning authority must match it. Unknown patient: 404 |
| `lowerDateTime`, `upperDateTime` | Yes | XML Schema dateTime or date. A value without a time zone is read in the site's configured time zone (`ihe.timezone`) |
| `mostRecentResults` | Yes | The *n* most recent ECGs |

The list response carries `Expires: 0`, so it is never cached. Each entry links
to the matching CARD-6 request.

**CARD-6 request parameters.**

| Parameter | Supported | Behaviour |
|---|---|---|
| `requestType=DOCUMENT` | Yes | |
| `documentUID` | UUID only | Documents are identified by UUID. An OID, which the profile also allows, is never issued by ECG Hub and returns 404 |
| `preferredContentType` | Yes | `application/pdf` is served. For `image/svg+xml`, the PDF is returned if the `Accept` header allows it, and 406 otherwise. Any other value: 406 |

The document response carries `Expires` set one week ahead, the maximum the
profile allows.

**Document content (CARD TF-2 §4.6.4.2.2).** PDF drawn with vector graphics,
not rasterised, by ecg-bridge v1.4.0. It has a 1 mm grid at 25 mm/s and
10 mm/mV, a calibration pulse, lead labels and transition marks, and statements
of frequency content and interpretation status. An explicit statement is
printed when either is absent. The patient name and identifier written on the
document are those held by ECG Hub (from the HIS when enrichment has run), not
those recorded by the device.

**Anonymous documents.** As §4.6.4.2.2.1 requires, ECG Hub **refuses** to serve
a document that would carry no patient name or identifier (406, with the reason
in the body). It never serves a degraded version.

**Errors.** As ITI-11 §3.11.4.1.3 requires, the explanation goes in the
response body. Go's HTTP server cannot customise the reason phrase.

**Security.**

- Dedicated listener (default port 8443), separate from the application API.
  TLS 1.2 minimum, **client certificate required and verified** against a
  configured CA. The service does not start if the certificate, key or client
  CA is missing.
- The transactions carry no authentication of their own, as specified. Mutual
  TLS is the whole access-control model.
- Every document retrieval is written to the **application audit log**, under
  the common name of the client certificate.
- **ATNA is not claimed.** The mutual TLS is close to Authenticate Node
  [ITI-19] in practice, but no audit messages are sent to an Audit Record
  Repository ([ITI-20]) and no Consistent Time actor is grouped.

### 3.2 Patient Update [RAD-12] — receiving role (no actor claimed)

| Aspect | Behaviour |
|---|---|
| Transport | HL7 v2 over MLLP, inbound listener (default port 2576), **off by default**. No TLS |
| Acknowledgement | An ACK is returned for **every** message, including malformed ones, so the sender never blocks |
| `ADT^A08` Update Patient Information | Applied to the matching patient. A field omitted keeps its stored value, and a field sent as `""` clears it (§4.12.4.3.2). PID-3 identifies the record and is never written |
| `ADT^A40` Merge Patient | PID-3 is the surviving identifier and MRG-1 the retired one. ECGs, tags and pins move to the survivor |
| `ADT^A02`, `A03`, `A06`, `A07` | Acknowledged and not applied. ECG Hub stores no visit, location or encounter data |
| Other triggers | Acknowledged and not applied |
| Replay protection | An A08 older than the last one applied to that patient (by EVN-2, falling back to MSH-7) is ignored |
| Sender restriction | Optional allowlists by source address and by sending facility (MSH-4). Both are empty by default |
| Field mapping | Shared with the outbound query path, configured in Admin > HL7 > Field mapping. Without an active mapping, messages are refused with `AE` |

**Deviations from the framework.**

| # | Deviation | Reason |
|---|---|---|
| R-1 | An A08 or A40 for a patient ECG Hub does not hold is acknowledged and **ignored**. The framework expects the surviving patient of an A40 to be created when the prior one is unknown | Patients are created when an ECG arrives. Creating them from the ADT feed would fill the database with the hospital's whole population |
| R-2 | Updates are applied to the database only. They are **not** written into stored ECG files and **not** sent on to downstream systems (archive, HIS) | See the DICOM Conformance Statement, Annex B.2 |
| R-3 | No TLS on the MLLP listener | Protect it at network level (`DAT.md` §7.2) |

---

## 4. Profiles and actors explicitly not supported

Listed because they are what an establishment is likely to ask about for an ECG
system.

| Profile | Actor(s) | Why not |
|---|---|---|
| Resting ECG Workflow (REWF) | Any | No Modality Worklist, MPPS, Storage Commitment or Query/Retrieve. ECGs are pushed by devices, with no order |
| Displayable Reports (DRPT) | Any | No DICOM encapsulated PDF or SR is created |
| Scheduled Workflow (SWF) | Any | See §1 |
| Patient Demographics Query (PDQ) | Patient Demographics Consumer | Demographics are queried with a classic `QRY^A19`, not with ITI-21 `QBP^Q22` (§5) |
| Patient Identifier Cross-referencing (PIX) | Any | A single patient identifier domain per installation (`DAT.md` §4) |
| Audit Trail and Node Authentication (ATNA) | Secure Node / Secure Application | See §3.1, Security |
| Consistent Time (CT) | Time Client | Time is synchronised by the host, which is not an IHE claim |
| Cross-Enterprise Document Sharing (XDS.b) | Any | Not implemented |

---

## 5. Non-IHE interfaces

For completeness. None of these is an IHE transaction.

| Interface | Direction | Standard | Purpose |
|---|---|---|---|
| Patient query | Outbound | HL7 v2.5 `QRY^A19` over MLLP | Fetch demographics from the HIS when an ECG arrives |
| Result | Outbound | HL7 v2.5 `ORU^R01`, with the PDF optionally embedded in `OBX` type `ED` | Send the ECG to the HIS, automatically or on demand |
| DICOM | Inbound and outbound | DICOM C-STORE / C-ECHO | See the DICOM Conformance Statement |
| Device ingestion | Inbound | FTP/FTPS, ECTP | Vendor formats from electrocardiographs |
| REST API, webhooks | Both | HTTPS / JSON | Third-party integration |
