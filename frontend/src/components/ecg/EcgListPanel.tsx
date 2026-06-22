import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { useECGs } from "../../hooks/useECGs";
import { EcgRow } from "./EcgRow";
import { Spinner } from "../ui/Spinner";
import { EmptyState } from "../ui/EmptyState";
import { fetchECGFilterFacets, type ECGFilters } from "../../lib/api";
import type { Patient } from "../../types";

interface Props {
  patient: Patient;
  selectedECGs: Set<number>;
  onToggleECG: (ecgId: number) => void;
  onToggleMultipleECGs: (ecgIds: number[], selected: boolean) => void;
  canForceHL7?: boolean;
  canDelete?: boolean;
  canRead?: boolean;
  canWrite?: boolean;
}

export function EcgListPanel({
  patient,
  selectedECGs,
  onToggleECG,
  onToggleMultipleECGs,
  canForceHL7,
  canDelete,
  canRead,
  canWrite,
}: Props) {
  const { t } = useTranslation();
  const [filters, setFilters] = useState<ECGFilters>({});
  const { ecgs, total, isLoading } = useECGs(patient.id, filters);
  const { data: facets } = useQuery({
    queryKey: ["ecg-filter-facets"],
    queryFn: fetchECGFilterFacets,
    staleTime: 5 * 60_000,
  });

  function handleFilterChange(patch: Partial<ECGFilters>) {
    setFilters((prev) => ({ ...prev, ...patch }));
  }

  return (
    <div className="border border-border ml-5 -mt-px">
      {/* Filter bar */}
      <div className="bg-muted/30 px-6 py-2 flex items-center gap-3 border-b border-border flex-wrap">
        {!isLoading && ecgs.length > 0 && (
          <input
            type="checkbox"
            checked={ecgs.every((e) => selectedECGs.has(e.id))}
            onChange={(e) =>
              onToggleMultipleECGs(
                ecgs.map((ecg) => ecg.id),
                e.target.checked,
              )
            }
            className="w-4 h-4 rounded border-border accent-primary cursor-pointer"
            aria-label={t("ecg.selectAll")}
            title={t("ecg.selectAll")}
          />
        )}
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
          <option value="">{t("filters.allVendors")}</option>
          {facets?.vendors.map((v) => (
            <option key={v} value={v}>
              {v}
            </option>
          ))}
        </select>
        <select
          value={filters.device_model ?? ""}
          onChange={(e) =>
            handleFilterChange({ device_model: e.target.value || undefined })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        >
          <option value="">{t("filters.allModels")}</option>
          {facets?.device_models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <select
          value={filters.file_format ?? ""}
          onChange={(e) =>
            handleFilterChange({ file_format: e.target.value || undefined })
          }
          className="text-xs bg-card border border-border rounded px-2 py-1 focus:outline-none focus:ring-1 focus:ring-ring/20"
        >
          <option value="">{t("filters.allFormats")}</option>
          {facets?.file_formats?.map((fmt) => (
            <option key={fmt} value={fmt}>
              .{fmt}
            </option>
          ))}
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
