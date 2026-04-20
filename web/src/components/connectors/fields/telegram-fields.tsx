import { useFormContext, type FieldErrors, type FieldError as RHFFieldError } from "react-hook-form";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { FieldError, SecretInput } from "../form-primitives";

type ConfigErrors =
  | {
      api_id?: RHFFieldError;
      api_hash?: RHFFieldError;
      phone?: RHFFieldError;
    }
  | undefined;

export function TelegramFields({ mode }: Readonly<{ mode: "create" | "edit" }>) {
  const { register, formState } = useFormContext();
  const errors = (formState.errors as FieldErrors).config as ConfigErrors;

  return (
    <div className="space-y-4">
      <div className="rounded-lg border border-border bg-muted/30 p-3 text-[12px] text-muted-foreground">
        <p className="mb-1 text-foreground/80">
          You&apos;ll need Telegram API credentials from{" "}
          <a
            href="https://my.telegram.org"
            target="_blank"
            rel="noreferrer"
            className="text-primary underline-offset-2 hover:underline"
          >
            my.telegram.org
          </a>
          {"."}
        </p>
        <p>
          Authentication happens after you save the connector — you&apos;ll get a code in your
          Telegram app.
        </p>
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="tg-apiid">API ID</Label>
          <Input
            id="tg-apiid"
            type="number"
            {...register("config.api_id", { valueAsNumber: true })}
          />
          <FieldError message={errors?.api_id?.message as string | undefined} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="tg-apihash">API hash</Label>
          <SecretInput
            id="tg-apihash"
            {...register("config.api_hash")}
            maskedPlaceholder={mode === "edit" ? "••••••••" : "32-character hex"}
          />
          <FieldError message={errors?.api_hash?.message as string | undefined} />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="tg-phone">Phone</Label>
        <Input
          id="tg-phone"
          placeholder="+15551234567"
          className="font-mono text-[13px]"
          {...register("config.phone")}
        />
        <FieldError message={errors?.phone?.message as string | undefined} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="tg-chats">Chat filter (optional)</Label>
        <Input
          id="tg-chats"
          placeholder="leave empty to index everything"
          {...register("config.chat_filter")}
        />
        <p className="text-[12px] text-muted-foreground">
          Comma-separated chat names or IDs. Empty = all chats.
        </p>
      </div>
    </div>
  );
}
