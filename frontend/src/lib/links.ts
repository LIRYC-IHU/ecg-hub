// Links a user can paste, send or print (#127). They are the URLs the patient
// page already keeps its selection in (?patient=, ?ecg=, since #129), on the
// origin the browser is using, so following one opens exactly what was shared.
//
// An ECG link carries its patient too. The viewer prints that patient's
// identity above the trace, and the server refuses to serve the waveform when
// the ECG is not filed under them, so a link cannot pair one person's name with
// another's recording.

export function patientLink(patientId: string): string {
  return `${window.location.origin}/?${new URLSearchParams({ patient: patientId })}`;
}

export function ecgLink(patientId: string, ecgId: string): string {
  return `${window.location.origin}/?${new URLSearchParams({ patient: patientId, ecg: ecgId })}`;
}
