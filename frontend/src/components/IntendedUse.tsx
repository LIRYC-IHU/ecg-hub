import { useTranslation } from "react-i18next";
import { Info } from "lucide-react";

/**
 * The project's declared intended use.
 *
 * This wording is load-bearing: the same statement appears in the README, in
 * the generated PDF report and in the filing made to the ANSM, and what a
 * regulator assesses is reasonably foreseeable use rather than documentation
 * alone. It therefore has to be visible to the people actually using the
 * software, not only to whoever reads the repository.
 *
 * Keep the three copies identical. Changing one means changing all of them,
 * and that is a decision recorded in the issue tracker — see AGENTS.md §2.2
 * and §7.
 *
 * `inline` is the one-line reminder carried on every screen; `full` is the
 * complete statement, shown on the system page.
 */
export function IntendedUse({ variant = "full" }: { variant?: "full" | "inline" }) {
  const { t } = useTranslation();

  if (variant === "inline") {
    return (
      <p className="text-[11px] leading-tight text-muted-foreground">
        {t("intendedUse.short")}
      </p>
    );
  }

  return (
    <section
      aria-labelledby="intended-use-heading"
      className="rounded-lg border border-border bg-muted/30 p-4"
    >
      <div className="flex items-start gap-3">
        <Info className="w-4 h-4 mt-0.5 shrink-0 text-muted-foreground" />
        <div className="space-y-2">
          <h3
            id="intended-use-heading"
            className="text-sm font-semibold text-foreground"
          >
            {t("intendedUse.title")}
          </h3>
          <p className="text-sm leading-relaxed text-muted-foreground">
            {t("intendedUse.statement")}
          </p>
          <p className="text-xs leading-relaxed text-muted-foreground">
            {t("intendedUse.status")}
          </p>
        </div>
      </div>
    </section>
  );
}
