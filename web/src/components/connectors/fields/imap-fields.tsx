import { useFormContext, type FieldErrors, type FieldError as RHFFieldError } from "react-hook-form";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { FieldError, SecretInput } from "../form-primitives";

type ConfigErrors =
  | {
      server?: RHFFieldError;
      port?: RHFFieldError;
      username?: RHFFieldError;
      password?: RHFFieldError;
      folders?: RHFFieldError;
    }
  | undefined;

export function ImapFields({ mode }: Readonly<{ mode: "create" | "edit" }>) {
  const { register, formState } = useFormContext();
  const errors = (formState.errors as FieldErrors).config as ConfigErrors;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-[1fr_120px] gap-3">
        <div className="space-y-1.5">
          <Label htmlFor="imap-server">Server</Label>
          <Input id="imap-server" placeholder="imap.fastmail.com" {...register("config.server")} />
          <FieldError message={errors?.server?.message as string | undefined} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="imap-port">Port</Label>
          <Input id="imap-port" type="number" {...register("config.port", { valueAsNumber: true })} />
        </div>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="imap-user">Username</Label>
        <Input id="imap-user" autoComplete="off" {...register("config.username")} />
        <FieldError message={errors?.username?.message as string | undefined} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="imap-pw">Password</Label>
        <SecretInput
          id="imap-pw"
          {...register("config.password")}
          maskedPlaceholder={mode === "edit" ? "••••••••" : "app-specific password"}
        />
        <FieldError message={errors?.password?.message as string | undefined} />
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="imap-folders">Folders</Label>
        <Input
          id="imap-folders"
          placeholder="INBOX,Sent"
          className="font-mono text-[13px]"
          {...register("config.folders")}
        />
        <p className="text-[12px] text-muted-foreground">Comma-separated mailboxes.</p>
      </div>
    </div>
  );
}
