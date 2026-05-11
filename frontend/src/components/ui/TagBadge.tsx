import { X } from "lucide-react";

function getContrastColor(hex: string | undefined | null): string {
  if (!hex) return "#ffffff";
  const c = hex.replace("#", "");
  if (c.length < 6) return "#ffffff";
  const r = parseInt(c.substring(0, 2), 16);
  const g = parseInt(c.substring(2, 4), 16);
  const b = parseInt(c.substring(4, 6), 16);
  const luminance = (0.299 * r + 0.587 * g + 0.114 * b) / 255;
  return luminance > 0.6 ? "#000000" : "#ffffff";
}

interface TagBadgeProps {
  name: string;
  color: string;
  onRemove?: () => void;
  size?: "sm" | "md";
}

export function TagBadge({ name, color = "#6b7280", onRemove, size = "md" }: TagBadgeProps) {
  const textColor = getContrastColor(color);
  const sizeClasses = size === "sm"
    ? "text-[10px] px-1.5 py-0.5 gap-0.5"
    : "text-xs px-2 py-0.5 gap-1";

  return (
    <span
      className={`inline-flex items-center rounded-full font-medium ${sizeClasses}`}
      style={{ backgroundColor: color, color: textColor }}
    >
      {name}
      {onRemove && (
        <button
          onClick={(e) => {
            e.stopPropagation();
            onRemove();
          }}
          className="rounded-full hover:opacity-70 transition-opacity"
          style={{ color: textColor }}
        >
          <X className={size === "sm" ? "w-2.5 h-2.5" : "w-3 h-3"} />
        </button>
      )}
    </span>
  );
}
