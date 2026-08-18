// Patient display helpers.

/** Minimal shape needed to name a patient — matches Patient and the quarantine search result. */
export interface NameablePatient {
  patient_id: string;
  first_name?: string | null;
  last_name?: string | null;
}

/**
 * Display name for a patient, in "Last, First" order.
 *
 * Demographics are empty between ingestion and HL7 enrichment, permanently for
 * patients the HIS does not know, and always on sites running without HL7 — so
 * never format the separator blindly. Falls back to the identifier rather than
 * rendering a bare ", ".
 */
export function formatPatientName(patient: NameablePatient): string {
  const last = patient.last_name?.trim() ?? "";
  const first = patient.first_name?.trim() ?? "";

  if (last && first) return `${last}, ${first}`;
  if (last) return last;
  if (first) return first;
  return patient.patient_id.trim() || "—";
}
