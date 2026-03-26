import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useECGs } from "../../hooks/useECGs";
import { EcgRow } from "./EcgRow";
import { Spinner } from "../ui/Spinner";
import { EmptyState } from "../ui/EmptyState";
import type { ECGFilters } from "../../lib/api";
import type { Patient } from "../../types";

interface Props {
  patient: Patient;
  selectedECGs: Set<number>;
  onToggleECG: (ecgId: number) => void;
  canForceHL7?: boolean;
  canDelete?: boolean;
  canRead?: boolean;
  canWrite?: boolean;
}

export function EcgListPanel({
  patient,
  selectedECGs,
  onToggleECG,
  canForceHL7,
  canDelete,
  canRead,
  canWrite,
}: Props) {
  const { t } = useTranslation();
  const [filters, setFilters] = useState<ECGFilters>({});
  const { ecgs, total, isLoading } = useECGs(patient.id, filters);

  function handleFilterChange(patch: Partial<ECGFilters>) {
    setFilters((prev) => ({ ...prev, ...patch }));
  }

  return (
    <div className="border border-border ml-5 -mt-px">
      {/* Filter bar */}
      <div className="bg-muted/30 px-6 py-2 flex items-center gap-3 border-b border-border flex-wrap">
        <input
          type="date"
          value={filters.from ?? ""}
          onChange={(e) =>
            handleFilterChange({ from: e.target.value || undefined })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        />
        <input
          type="date"
          value={filters.to ?? ""}
          onChange={(e) =>
            handleFilterChange({ to: e.target.value || undefined })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        />
        <select
          value={filters.vendor ?? ""}
          onChange={(e) =>
            handleFilterChange({ vendor: e.target.value || undefined })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        >
          <option value="">Toutes sources</option>
          <option value="dicom">DICOM</option>
          <option value="philips">Philips</option>
        </select>
        <select
          value={filters.hl7_status ?? ""}
          onChange={(e) =>
            handleFilterChange({
              hl7_status:
                (e.target.value as ECGFilters["hl7_status"]) || undefined,
            })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        >
          <option value="">{t("ecg.hl7Status")}</option>
          <option value="pending">{t("ecg.status.pending")}</option>
          <option value="success">{t("ecg.status.success")}</option>
          <option value="hl7_exhausted">{t("ecg.status.hl7_exhausted")}</option>
        </select>
      </div>

      {isLoading && (
        <div className="flex justify-center py-6">
          <Spinner size={18} className="text-muted-foreground" />
        </div>
      )}
      {!isLoading && ecgs.length === 0 && <EmptyState type="ecg" />}
      {!isLoading && ecgs.length > 0 && (
        <>
          {ecgs.map((ecg) => (
            <EcgRow
              key={ecg.id}
              ecg={ecg}
              isSelected={selectedECGs.has(ecg.id)}
              onToggle={() => onToggleECG(ecg.id)}
              canForceHL7={canForceHL7}
              canDelete={canDelete}
              canRead={canRead}
              canWrite={canWrite}
              patientId={patient.id}
            />
          ))}
          {total > ecgs.length && (
            <p className="text-xs text-muted-foreground px-6 py-2 text-right border-t border-border">
              {ecgs.length} / {total}
            </p>
          )}
        </>
      )}
    </div>
  );
}
