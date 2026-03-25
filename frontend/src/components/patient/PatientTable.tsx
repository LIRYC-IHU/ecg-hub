import { useState } from "react";
import { PatientRow } from "./PatientRow";
import { EcgListPanel } from "../ecg/EcgListPanel";
import { Spinner } from "../ui/Spinner";
import { EmptyState } from "../ui/EmptyState";
import type { Patient } from "../../types";
import { useTranslation } from "react-i18next";

interface PatientTableProps {
  patients: Patient[];
  isLoading: boolean;
  selectedECGs: Set<number>;
  onToggleECG: (ecgId: number) => void;
  canDelete?: boolean;
  canForceHL7?: boolean;
  canRead?: boolean;
  canWrite?: boolean;
}

export function PatientTable({
  patients,
  isLoading,
  selectedECGs,
  onToggleECG,
  canDelete,
  canForceHL7,
  canRead,
  canWrite,
}: PatientTableProps) {
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const { t } = useTranslation();

  if (isLoading)
    return (
      <div className="flex justify-center py-16">
        <Spinner size={24} className="text-muted-foreground" />
      </div>
    );

  if (patients.length === 0) return <EmptyState type="patients" />;

  return (
    <div className="bg-card rounded-lg border border-border overflow-hidden">
      {/* Header */}
      <div className="grid grid-cols-[1fr_160px_80px_160px_32px] gap-4 px-4 py-2.5 bg-muted/50 border-b border-border">
        <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          {t("patient.colName")}
        </span>
        <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          {t("patient.colId")}
        </span>
        <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          {t("patient.colEcgs")}
        </span>
        <span className="text-[11px] font-semibold text-muted-foreground uppercase tracking-wider">
          {t("patient.colLastActivity")}
        </span>
      </div>

      {patients.map((patient) => (
        <div key={patient.id}>
          <PatientRow
            patient={patient}
            isExpanded={expandedId === patient.id}
            onClick={() =>
              setExpandedId(expandedId === patient.id ? null : patient.id)
            }
          />
          {expandedId === patient.id && (
            <EcgListPanel
              patient={patient}
              selectedECGs={selectedECGs}
              onToggleECG={onToggleECG}
              canForceHL7={canForceHL7}
              canDelete={canDelete}
              canRead={canRead}
              canWrite={canWrite}
            />
          )}
        </div>
      ))}
    </div>
  );
}
