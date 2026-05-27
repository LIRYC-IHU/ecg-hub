import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Upload, X, Building2 } from "lucide-react";
import { fetchBranding, saveBranding, uploadLogo } from "../../lib/api";
import { Spinner } from "../ui/Spinner";
import { useNotification } from "../../context/NotificationContext";
import { BRANDING_FALLBACK } from "../../hooks/useBranding";

export function AdminBrandingPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { notify } = useNotification();
  const fileRef = useRef<HTMLInputElement>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["branding"],
    queryFn: fetchBranding,
    staleTime: 60_000,
  });

  const [centerName, setCenterName] = useState<string | undefined>(undefined);
  const [previewLogo, setPreviewLogo] = useState<string | undefined>(undefined);
  const [pendingFile, setPendingFile] = useState<File | undefined>(undefined);

  const effectiveName = centerName ?? data?.center_name ?? "";
  const effectiveLogo = previewLogo ?? data?.logo_base64 ?? "";

  const saveMutation = useMutation({
    mutationFn: async () => {
      let logo: string | undefined;
      if (pendingFile) {
        logo = await uploadLogo(pendingFile);
      }
      await saveBranding(effectiveName, logo ?? data?.logo_base64);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["branding"] });
      notify("success", t("admin.branding.saved"));
      setCenterName(undefined);
      setPreviewLogo(undefined);
      setPendingFile(undefined);
    },
    onError: () => notify("error", t("common.error")),
  });

  const removeLogo = useMutation({
    mutationFn: () => saveBranding(effectiveName, ""),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["branding"] });
      notify("success", t("admin.branding.logoRemoved"));
      setPreviewLogo(undefined);
      setPendingFile(undefined);
    },
    onError: () => notify("error", t("common.error")),
  });

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    if (!file) return;
    setPendingFile(file);
    const reader = new FileReader();
    reader.onload = (ev) => setPreviewLogo(ev.target?.result as string);
    reader.readAsDataURL(file);
    // reset input so same file can be re-selected
    e.target.value = "";
  }

  const isDirty =
    (centerName !== undefined && centerName !== data?.center_name) ||
    pendingFile !== undefined;

  return (
    <div className="p-6 max-w-xl">
      <div className="mb-6">
        <h1 className="text-lg font-semibold text-foreground">
          {t("admin.branding.title")}
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          {t("admin.branding.subtitle")}
        </p>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-12">
          <Spinner size={18} className="text-muted-foreground" />
        </div>
      ) : (
        <div className="space-y-6">
          {/* Center name */}
          <div className="bg-card border border-border rounded-lg p-4">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-3">
              {t("admin.branding.centerName")}
            </label>
            <div className="relative">
              <Building2 className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
              <input
                type="text"
                value={effectiveName}
                onChange={(e) => setCenterName(e.target.value)}
                placeholder={BRANDING_FALLBACK}
                className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/40"
              />
            </div>
            <p className="text-[11px] text-muted-foreground mt-2">
              {t("admin.branding.centerNameHint")}
            </p>
          </div>

          {/* Logo */}
          <div className="bg-card border border-border rounded-lg p-4">
            <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider block mb-3">
              {t("admin.branding.logo")}
            </label>

            <div className="flex items-center gap-4">
              {/* Preview */}
              <div className="w-16 h-16 rounded-xl bg-white flex items-center justify-center overflow-hidden shrink-0">
                {effectiveLogo ? (
                  <img
                    src={effectiveLogo}
                    alt="logo preview"
                    className="w-full h-full object-contain p-1.5"
                  />
                ) : (
                  <Building2 className="w-7 h-7 text-primary-foreground/60" />
                )}
              </div>

              <div className="flex flex-col gap-2">
                <button
                  onClick={() => fileRef.current?.click()}
                  className="flex items-center gap-2 text-xs border border-border rounded-lg px-3 py-2 hover:bg-muted/50 transition-colors"
                >
                  <Upload className="w-3.5 h-3.5" />
                  {t("admin.branding.uploadLogo")}
                </button>
                {effectiveLogo && (
                  <button
                    onClick={() => removeLogo.mutate()}
                    disabled={removeLogo.isPending}
                    className="flex items-center gap-2 text-xs text-destructive border border-destructive/20 rounded-lg px-3 py-2 hover:bg-destructive/5 transition-colors disabled:opacity-50"
                  >
                    {removeLogo.isPending ? (
                      <Spinner size={11} />
                    ) : (
                      <X className="w-3.5 h-3.5" />
                    )}
                    {t("admin.branding.removeLogo")}
                  </button>
                )}
              </div>
            </div>

            <p className="text-[11px] text-muted-foreground mt-3">
              {t("admin.branding.logoHint")}
            </p>

            <input
              ref={fileRef}
              type="file"
              accept="image/png,image/jpeg,image/svg+xml,image/webp"
              className="hidden"
              onChange={handleFileChange}
            />
          </div>

          {/* Save */}
          {isDirty && (
            <div className="flex gap-3">
              <button
                onClick={() => saveMutation.mutate()}
                disabled={saveMutation.isPending}
                className="flex items-center gap-1.5 bg-primary text-primary-foreground text-sm font-medium px-4 py-2 rounded-lg hover:bg-primary/90 disabled:opacity-50 transition-colors"
              >
                {saveMutation.isPending && (
                  <Spinner size={12} className="text-primary-foreground" />
                )}
                {t("modules.save")}
              </button>
              <button
                onClick={() => {
                  setCenterName(undefined);
                  setPreviewLogo(undefined);
                  setPendingFile(undefined);
                }}
                className="text-sm text-muted-foreground hover:text-foreground transition-colors"
              >
                {t("common.cancel")}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
