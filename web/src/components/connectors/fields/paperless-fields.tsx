import { useFormContext, type FieldErrors, type FieldError as RHFFieldError } from "react-hook-form";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { FieldError, SecretInput } from "../form-primitives";

type ConfigErrors = { url?: RHFFieldError; token?: RHFFieldError } | undefined;

export function PaperlessFields({ mode }: Readonly<{ mode: "create" | "edit" }>) {
  const { register, formState } = useFormContext();
  const errors = (formState.errors as FieldErrors).config as ConfigErrors;

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="pp-url">Paperless URL</Label>
        <Input
          id="pp-url"
          placeholder="http://paperless.home:8000"
          {...register("config.url")}
        />
        <FieldError message={errors?.url?.message as string | undefined} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="pp-token">API token</Label>
        <SecretInput
          id="pp-token"
          {...register("config.token")}
          maskedPlaceholder={mode === "edit" ? "••••••••" : "40-character token"}
        />
        <FieldError message={errors?.token?.message as string | undefined} />
        <p className="text-[12px] text-muted-foreground">
          Generate from Paperless-ngx → User profile → API auth token.
        </p>
      </div>
    </div>
  );
}
