// Exam date display helpers.

export interface ExamDate {
  /** Formatted date to show. */
  text: string;
  /**
   * False when the device gave us no acquisition date and the text is the
   * import date standing in for it. Callers must mark that case in the UI —
   * an import date rendered as an exam date is silently wrong clinical data.
   */
  isRecorded: boolean;
}

const DATE_TIME: Intl.DateTimeFormatOptions = {
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
  hour: "2-digit",
  minute: "2-digit",
};

/**
 * Exam date for an ECG.
 *
 * `recorded_at` is null whenever the source file carries no acquisition
 * timestamp — routine for DICOM exports that leave StudyDate and its
 * fallbacks empty. The import date is shown instead, flagged as such.
 */
export function examDate(
  ecg: { recorded_at?: string | null; ingested_at?: string | null },
  locale = "fr-FR",
): ExamDate {
  const recorded = ecg.recorded_at ?? "";
  const iso = recorded || ecg.ingested_at || "";
  if (iso === "") return { text: "—", isRecorded: false };

  return {
    text: new Date(iso).toLocaleString(locale, DATE_TIME),
    isRecorded: recorded !== "",
  };
}
