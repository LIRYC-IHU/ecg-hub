import { ChevronRight } from "lucide-react";
import type { Patient } from "../../types";

interface PatientRowProps {
  patient: Patient;
  isExpanded: boolean;
  onClick: () => void;
}

function formatLastActivity(iso: string | null): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleDateString("fr-FR", {
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
  });
}

export function PatientRow({ patient, isExpanded, onClick }: PatientRowProps) {
  return (
    <div
      onClick={onClick}
      aria-expanded={isExpanded}
      className="w-full grid grid-cols-[1fr_160px_80px_160px_32px] gap-4 px-4 py-3 items-center hover:bg-muted/30 transition-colors text-left border-b border-l border-r border-t rounded-sm border-border cursor-pointer"
    >
      <span className="text-sm font-medium text-foreground">
        {patient.last_name}, {patient.first_name}
      </span>
      <span className="text-xs font-mono bg-muted text-muted-foreground px-2 py-0.5 rounded w-fit">
        {patient.patient_id}
      </span>
      <span className="text-sm text-foreground">{patient.ecg_count ?? 0}</span>
      <span className="text-sm text-muted-foreground">
        {formatLastActivity(patient.last_activity)}
      </span>
      <ChevronRight
        className={`w-4 h-4 text-muted-foreground transition-transform duration-200 ${
          isExpanded ? "rotate-90" : ""
        }`}
      />
    </div>
  );
}
