import { useTranslation } from "react-i18next";
import { examDate } from "../../lib/ecgDate";

interface Props {
  ecg: { recorded_at?: string | null; ingested_at?: string | null };
  className?: string;
}

/**
 * Exam date of an ECG.
 *
 * When the source file carries no acquisition timestamp the import date is
 * shown in its place, marked so it cannot be mistaken for the real one.
 */
export function ExamDate({ ecg, className = "" }: Props) {
  const { t } = useTranslation();
  const date = examDate(ecg);

  return (
    <span className={className}>
      {date.text}
      {!date.isRecorded && (
        <span
          className="ml-1 text-[10px] uppercase tracking-wide text-warning"
          title={t("ecg.dateFromImportHint")}
        >
          {t("ecg.dateFromImport")}
        </span>
      )}
    </span>
  );
}
