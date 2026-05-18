import { useState, useRef, useEffect } from "react";
import { Plus, Check, Trash2 } from "lucide-react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  fetchTags,
  fetchPatientTags,
  tagPatient,
  untagPatient,
  fetchECGTags,
  tagECG,
  untagECG,
  createTag,
  deleteTag,
  type TagDTO,
} from "../../lib/api";

const PRESET_COLORS = [
  "#ef4444",
  "#f59e0b",
  "#22c55e",
  "#3b82f6",
  "#8b5cf6",
  "#ec4899",
  "#6b7280",
];

interface TagManagerProps {
  patientId?: string;
  ecgId?: string;
  canCreate?: boolean;
  canDelete?: boolean;
  canApply?: boolean;
}

export function TagManager({ patientId, ecgId, canCreate = true, canDelete = false, canApply = true }: TagManagerProps) {
  const entityId = patientId || ecgId || "";
  const isECG = !!ecgId;
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [newTagName, setNewTagName] = useState("");
  const [newTagColor, setNewTagColor] = useState(PRESET_COLORS[0]);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | null>(null);
  const popoverRef = useRef<HTMLDivElement>(null);

  const { data: allTags = [] } = useQuery({
    queryKey: ["tags"],
    queryFn: fetchTags,
    staleTime: 60_000,
  });

  const queryKey = isECG ? ["ecg-tags", entityId] : ["patient-tags", entityId];

  const { data: entityTags = [] } = useQuery({
    queryKey,
    queryFn: () => isECG ? fetchECGTags(entityId) : fetchPatientTags(entityId),
    staleTime: 30_000,
    enabled: !!entityId,
  });

  const patientTagIds = new Set(entityTags.map((t) => t.id));

  const tagMutation = useMutation({
    mutationFn: (tagId: string) => isECG ? tagECG(entityId, tagId) : tagPatient(entityId, tagId),
    onMutate: async (tagId) => {
      await queryClient.cancelQueries({ queryKey });
      const previous = queryClient.getQueryData<TagDTO[]>(queryKey);
      const tagToAdd = allTags.find((t) => t.id === tagId);
      if (tagToAdd) {
        queryClient.setQueryData<TagDTO[]>(queryKey, (old) => [...(old ?? []), tagToAdd]);
      }
      return { previous };
    },
    onError: (_err, _tagId, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKey, context.previous);
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey });
    },
  });

  const untagMutation = useMutation({
    mutationFn: (tagId: string) => isECG ? untagECG(entityId, tagId) : untagPatient(entityId, tagId),
    onMutate: async (tagId) => {
      await queryClient.cancelQueries({ queryKey });
      const previous = queryClient.getQueryData<TagDTO[]>(queryKey);
      queryClient.setQueryData<TagDTO[]>(queryKey, (old) => (old ?? []).filter((t) => t.id !== tagId));
      return { previous };
    },
    onError: (_err, _tagId, context) => {
      if (context?.previous) {
        queryClient.setQueryData(queryKey, context.previous);
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey });
    },
  });

  const createMutation = useMutation({
    mutationFn: () => createTag(newTagName.trim(), newTagColor),
    onSuccess: (newTag) => {
      setNewTagName("");
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
      if (canApply) tagMutation.mutate(newTag.id);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (tagId: string) => deleteTag(tagId),
    onSuccess: () => {
      setConfirmDeleteId(null);
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
      void queryClient.invalidateQueries({ queryKey });
    },
  });

  useEffect(() => {
    if (!open) return;
    function handleClickOutside(e: MouseEvent) {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node)) {
        setOpen(false);
        setConfirmDeleteId(null);
      }
    }
    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [open]);

  const handleToggle = (tagId: string) => {
    if (!canApply) return;
    if (patientTagIds.has(tagId)) {
      untagMutation.mutate(tagId);
    } else {
      tagMutation.mutate(tagId);
    }
  };

  return (
    <div className="relative inline-block">
      <button
        onClick={() => setOpen(!open)}
        className="inline-flex items-center justify-center w-6 h-6 rounded-full border border-dashed border-border text-muted-foreground hover:text-foreground hover:border-foreground/40 transition-colors"
        title={t("tags.manage", "Gérer les tags")}
      >
        <Plus className="w-3 h-3" />
      </button>

      {open && (
        <div
          ref={popoverRef}
          className="absolute top-full left-0 mt-1 z-50 w-64 rounded-lg border border-border bg-card shadow-lg"
        >
          <div className="p-2 border-b border-border">
            <span className="text-xs font-semibold text-foreground">
              {t("tags.title", "Tags")}
            </span>
          </div>

          <div className="max-h-48 overflow-y-auto p-1">
            {allTags.length === 0 && (
              <p className="text-xs text-muted-foreground text-center py-3">
                {t("tags.empty", "Aucun tag")}
              </p>
            )}
            {allTags.map((tag) => (
              <div
                key={tag.id}
                className="group flex items-center gap-2 px-2 py-1.5 rounded-md hover:bg-muted/50 transition-colors"
              >
                <button
                  onClick={() => handleToggle(tag.id)}
                  disabled={!canApply}
                  className="flex items-center gap-2 flex-1 min-w-0 text-left disabled:opacity-60"
                >
                  <span
                    className="w-3 h-3 rounded-full shrink-0"
                    style={{ backgroundColor: tag.color }}
                  />
                  <span className="flex-1 text-sm text-foreground truncate">
                    {tag.name}
                  </span>
                  {patientTagIds.has(tag.id) && (
                    <Check className="w-3.5 h-3.5 text-primary shrink-0" />
                  )}
                </button>
                {canDelete && (
                  confirmDeleteId === tag.id ? (
                    <div className="flex items-center gap-1 shrink-0">
                      <button
                        onClick={() => deleteMutation.mutate(tag.id)}
                        className="text-[10px] text-destructive font-medium hover:underline"
                      >
                        {t("common.confirm")}
                      </button>
                      <button
                        onClick={() => setConfirmDeleteId(null)}
                        className="text-[10px] text-muted-foreground hover:underline"
                      >
                        {t("common.cancel")}
                      </button>
                    </div>
                  ) : (
                    <button
                      onClick={() => setConfirmDeleteId(tag.id)}
                      className="shrink-0 p-0.5 rounded text-muted-foreground/0 group-hover:text-muted-foreground/60 hover:!text-destructive transition-colors"
                      title={t("common.delete")}
                    >
                      <Trash2 className="w-3 h-3" />
                    </button>
                  )
                )}
              </div>
            ))}
          </div>

          {canCreate && (
            <div className="p-2 border-t border-border space-y-2">
              <div className="flex gap-1.5">
                <input
                  type="text"
                  value={newTagName}
                  onChange={(e) => setNewTagName(e.target.value)}
                  placeholder={t("tags.newPlaceholder", "Nouveau tag…")}
                  className="flex-1 text-xs px-2 py-1.5 rounded-md border border-border bg-background focus:outline-none focus:ring-1 focus:ring-ring/20"
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && newTagName.trim()) {
                      createMutation.mutate();
                    }
                  }}
                />
                <button
                  onClick={() => newTagName.trim() && createMutation.mutate()}
                  disabled={!newTagName.trim() || createMutation.isPending}
                  className="px-2 py-1.5 rounded-md text-xs font-medium bg-primary text-primary-foreground hover:opacity-90 disabled:opacity-50 transition-opacity"
                >
                  {t("tags.add", "Ajouter")}
                </button>
              </div>
              <div className="flex gap-1">
                {PRESET_COLORS.map((color) => (
                  <button
                    key={color}
                    onClick={() => setNewTagColor(color)}
                    className={`w-5 h-5 rounded-full transition-transform ${
                      newTagColor === color ? "ring-2 ring-offset-1 ring-foreground/40 scale-110" : ""
                    }`}
                    style={{ backgroundColor: color }}
                  />
                ))}
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
