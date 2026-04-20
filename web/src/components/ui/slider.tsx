import { Slider as SliderPrimitive } from "@base-ui/react/slider";

import { cn } from "@/lib/utils";

interface SliderProps {
  value: number;
  onValueChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  disabled?: boolean;
  className?: string;
  "aria-label"?: string;
}

/**
 * Single-thumb numeric slider. Thin wrapper over @base-ui/react's Slider
 * primitive matched to the reading-room token palette (marmalade fill,
 * neutral track, subtle ring on focus).
 */
export function Slider({
  value,
  onValueChange,
  min = 0,
  max = 100,
  step = 1,
  disabled,
  className,
  ...rest
}: Readonly<SliderProps>) {
  return (
    <SliderPrimitive.Root
      value={value}
      onValueChange={(v) => {
        if (typeof v === "number") onValueChange(v);
        else if (Array.isArray(v) && typeof v[0] === "number") onValueChange(v[0]);
      }}
      min={min}
      max={max}
      step={step}
      disabled={disabled}
      aria-label={rest["aria-label"]}
      className={cn("relative flex w-full touch-none select-none items-center", className)}
    >
      <SliderPrimitive.Control className="relative flex h-5 w-full items-center">
        <SliderPrimitive.Track className="relative h-[3px] w-full grow overflow-hidden rounded-full bg-muted">
          <SliderPrimitive.Indicator className="absolute h-full bg-primary/70 data-[disabled]:bg-muted-foreground/30" />
        </SliderPrimitive.Track>
        <SliderPrimitive.Thumb
          className={cn(
            "block size-4 rounded-full border border-border bg-background shadow-[0_0_0_2px_var(--background)]",
            "ring-1 ring-border transition-colors",
            "hover:ring-primary/50 focus-visible:ring-3 focus-visible:ring-primary/40",
            "data-[disabled]:bg-muted-foreground/30 data-[disabled]:cursor-not-allowed",
          )}
        />
      </SliderPrimitive.Control>
    </SliderPrimitive.Root>
  );
}
