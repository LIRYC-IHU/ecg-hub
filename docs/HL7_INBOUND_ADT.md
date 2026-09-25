# Inbound ADT — receiving patient updates (IHE RAD-12)

ECG Hub can receive patient changes pushed by the HIS instead of only asking for
them. This is the receiving half of **IHE RAD-12 Patient Update**: the HIS sends
an ADT message, the hub applies it and acknowledges.

It is off by default. A deployment that does not want an inbound feed does not
get an open port.

```yaml
adt:
  enabled: true
  port: 2576
```

The two directions are independent. The outbound side (a QRY query when an ECG
arrives) is configured under `hl7:` in the admin UI and works with or without
this listener.

## What it does with each message

| Trigger | What happens |
|---|---|
| **A08** Update Patient Information | Demographics are applied to the matching patient |
| **A40** Merge Patient | The two records are merged |
| A02, A03, A06, A07 | Acknowledged, nothing applied |

A02/A03/A06/A07 carry transfers, discharges and patient-class changes. This
system stores no visit, location or encounter information, so there is nowhere
for them to go — they are acknowledged rather than rejected, because there is
nothing wrong with them and a rejection would only make the sender retry.

**Every message is acknowledged**, including one too malformed to route. An HL7
sender generally blocks or retries until it gets an ACK, so a silent drop is the
one outcome a feed cannot diagnose.

## How a message is read

The same field mappings the query path uses, configured in **Admin > HL7 > Field
mapping**. An ADT carries the same `PID` segment a query response does, so one
preset serves both.

The mappings that matter here:

| Target | Typical path | Notes |
|---|---|---|
| `patient_id` | `PID.3` | The record to update. Its first component is taken, so `PID.3` and `PID.3.1` both work |
| `last_name` | `PID.5.1` | |
| `first_name` | `PID.5.2` | |
| `date_of_birth` | `PID.7` | `YYYYMMDD` |
| `gender` | `PID.8` | |
| `nda` | `PID.18` | Patient account number |
| `prior_patient_id` | `MRG.1` | A40 only, and only if your feed puts it elsewhere — `MRG.1` is the default |

Without an active preset the listener refuses the message with `AE` rather than
guess. Applying an update it could not read would blank the record.

### Omitted is not the same as empty

RAD-12 §4.12.4.3.2 draws a line the hub follows exactly:

| The message sends | The stored value |
|---|---|
| nothing for a field | is left alone |
| `""` (two double quotes) | is **removed** |
| a value | is replaced |

So a feed that only ever sends the fields it changed will not erase everything
else, and a feed that deliberately clears a field can.

## A08 cannot change a patient identifier

The framework is explicit, and so is the implementation: an A08 updates
demographics, and **only an A40 may change an identifier**. `PID-3` on an A08 is
read as the record to look up, never as a value to write.

## A40 — which way round

This is the one to read twice.

```
PID-3   the surviving identifier   ("the dominant patient information")
MRG-1   the identifier to stop using
```

Read backwards, a merge files the surviving patient's traces under an identifier
the HIS has just retired. The hub moves the prior record onto the surviving one:
it renames when the surviving identifier is free, and merges into it when it is
taken — ECGs, tags and pins follow.

A replay is harmless. Once the prior identifier has been merged away no record
holds it, so the message does nothing.

## Patients this system does not hold

**Ignored, and acknowledged.** Patients here are created when an ECG arrives.
Creating them from an ADT feed would fill the table with the whole hospital, for
people the hub will never hold a trace for.

This is a deliberate deviation from the profile, which says to create the
surviving patient from an A40 when the prior one is unknown. It belongs in an
IHE Integration Statement if one is published.

## Replayed messages

An A08 older than the last one applied to that patient is ignored. The hub stores
the event time of the most recent update (`EVN-2`, or `MSH-7` when the message
carries no `EVN`) and compares.

The risk is not a feed delivering out of order — one channel on one connection
does not — but a **retransmission**: an ADT from three weeks ago replayed after a
failure would otherwise put the old name back.

`EVN-2` is preferred over `MSH-7` because a retransmission rebuilds the header
while the event stays where it was.

## Who may send

Two allowlists, both empty by default, so configuring nothing locks nobody out.
**They are not equivalent.**

```yaml
adt:
  allowed_senders: []      # IP addresses and CIDR ranges
  allowed_facilities: []   # MSH-4 sending facilities
```

### `allowed_senders` — real, but only without NAT

This is genuine access control, and it only means anything where the source
address reaches the listener intact.

Under Docker bridge networking every connection appears to come from the Docker
gateway. The list could then only accept the gateway — and therefore everything
behind it — while looking like access control. On some setups the observed
address bears no relation to the sender at all.

**Check before trusting it.** The listener logs the address it actually sees on
the first connection:

```
hl7 listener: first connection — this is the address the sender allowlist is
matched against   remote=172.21.0.1:44459
```

If that is a gateway, or something unexpected, this list is not the control you
want. It is worth setting on a host-network deployment
(`docker-compose.host.yml`), where the listener sees the sending system directly.

### `allowed_facilities` — survives NAT, proves less

`MSH-4` travels inside the message, so it reaches the listener whatever the
network does. For exactly that reason it proves less: the value is whatever the
sender chose to write.

It stops a feed pointed at the wrong system. It does not stop someone who can
reach the port.

**Neither replaces controlling who can reach the port.** This is a write path
into patient identity.

## Seeing what arrived

**Admin > HL7 > Inbound ADT** lists what was received and what became of it,
filterable by outcome.

| Outcome | Meaning |
|---|---|
| `applied` | A patient record changed |
| `ignored` | Valid, nothing here to change — an unknown patient, a trigger not acted on, a replay |
| `refused` | Turned away before being read — an unlisted sending facility |
| `error` | Understood, could not be applied |

The outcome and the acknowledgement answer different questions. An A08 that
changed a record and one for a patient the hub does not hold **both answer
`AA`** — only the outcome tells them apart.

The listing shows the segment names each message carried (`MSH,EVN,PID,PV1`),
which is what reveals a feed sending a PID without a PV1, or an A40 without its
MRG.

**No message body is stored.** An ADT carries the patient's name, date of birth
and address; this history exists to answer operational questions, not to become a
second copy of the demographics. Set `adt.history_retention_days` (30 by default,
`0` keeps everything) — on a hospital feed this is the table that fills the disk
first.

To watch a feed without letting it change anything, swap the handler for
`hl7.ObserveOnly` in `cmd/ecg-hub/main.go`. Everything is acknowledged and
logged, nothing is written.

## Testing it

`tools/adt-send.sh` sends a message and shows the acknowledgement:

```bash
tools/adt-send.sh a08              # demographics update
tools/adt-send.sh a40              # merge
tools/adt-send.sh unknown          # a trigger RAD-12 does not define
tools/adt-send.sh malformed        # not routable at all
tools/adt-send.sh file my.hl7      # your own

HOST=10.0.0.5 PORT=2576 tools/adt-send.sh a08
FACILITY=CHU_BORDEAUX tools/adt-send.sh a08   # exercise allowed_facilities
MERGE_FROM=MRN-BS1215 PATIENT=BS1215 tools/adt-send.sh a40
```

It exists because the framing is what goes wrong by hand: MLLP wraps the message
in `0x0B … 0x1C 0x0D`, segments are separated by carriage returns rather than
newlines, and an editor gives you the wrong one without saying so. A bare `nc`
also leaves you waiting for the reply — the listener holds the connection open
for the next message, so nothing closes it for you.

## Ports

| | |
|---|---|
| Default | `2576` |
| Override | `adt.port`, and `ADT_PORT` on the published mapping |
| Published by | `docker-compose.yml`, `docker-compose.dev.yml` |

`2576` sits next to the conventional `2575` used for outbound HL7, so the two do
not collide on a host network.

## What is not implemented

- **Creating a patient** from an ADT for someone the hub has never seen.
- **Visit and encounter information** — A02/A03/A06/A07 are acknowledged and
  dropped, because there is no column for a patient class or a location.
- **Configuration from the admin UI.** The listener is set up in `config.yaml`,
  like the IHE listener and unlike the ingestion modules. A port change needs a
  restart in any case.
