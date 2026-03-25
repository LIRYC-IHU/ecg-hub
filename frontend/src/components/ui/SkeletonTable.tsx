function SkeletonRow({ cols = 4 }: { cols?: number }) {
  return (
    <div className="flex items-center gap-4 px-4 py-3 border-b border-border">
      {Array.from({ length: cols }).map((_, i) => (
        <div
          key={i}
          className="skeleton h-4"
          style={{ width: `${20 + (i * 7) % 30}%`, maxWidth: i === 0 ? '160px' : '120px' }}
        />
      ))}
    </div>
  )
}

export function SkeletonTable({ rows = 6, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <div className="bg-card rounded-lg border border-border overflow-hidden">
      <div className="flex items-center gap-4 px-4 py-2.5 border-b border-border bg-muted/50">
        {Array.from({ length: cols }).map((_, i) => (
          <div key={i} className="skeleton h-3" style={{ width: `${60 + (i * 11) % 40}px` }} />
        ))}
      </div>
      {Array.from({ length: rows }).map((_, i) => (
        <SkeletonRow key={i} cols={cols} />
      ))}
    </div>
  )
}
