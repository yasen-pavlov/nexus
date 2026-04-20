interface Props {
  label?: string;
}

export function EdgeRail({ label = "Beginning of conversation" }: Readonly<Props>) {
  return (
    <div className="flex items-center gap-3 py-6" aria-hidden={false}>
      <div className="h-px flex-1 bg-border/60" aria-hidden />
      <span className="text-[10px] font-semibold uppercase tracking-[0.12em] text-muted-foreground/70">
        {label}
      </span>
      <div className="h-px flex-1 bg-border/60" aria-hidden />
    </div>
  );
}
