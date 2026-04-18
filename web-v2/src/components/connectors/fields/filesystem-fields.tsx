import { useFormContext, type FieldErrors, type FieldError as RHFFieldError } from "react-hook-form";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { FieldError } from "../form-primitives";

type ConfigErrors = { root_path?: RHFFieldError; patterns?: RHFFieldError } | undefined;

export function FilesystemFields() {
  const { register, formState } = useFormContext();
  const errors = (formState.errors as FieldErrors).config as ConfigErrors;

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="fs-root">Root path</Label>
        <Input
          id="fs-root"
          placeholder="/home/you/notes"
          className="font-mono text-[13px]"
          {...register("config.root_path")}
        />
        <p className="text-[12px] text-muted-foreground">
          Absolute path on the Nexus server. Read-only.
        </p>
        <FieldError message={errors?.root_path?.message as string | undefined} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="fs-patterns">File patterns</Label>
        <Input
          id="fs-patterns"
          placeholder="*.txt,*.md"
          className="font-mono text-[13px]"
          {...register("config.patterns")}
        />
        <p className="text-[12px] text-muted-foreground">
          Comma-separated globs. Defaults to <span className="font-mono">*.txt,*.md</span>.
        </p>
      </div>
    </div>
  );
}
