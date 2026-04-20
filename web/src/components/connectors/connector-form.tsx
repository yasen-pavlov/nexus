import { useEffect } from "react";
import { FormProvider, useForm, useWatch, type Resolver } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

import { ConnectorTypePicker, type ConnectorTypeKey } from "./type-picker";
import { ScheduleField } from "./schedule-field";
import { SyncWindowField } from "./sync-window-field";
import { FieldError, FormSection, ToggleRow } from "./form-primitives";
import { FilesystemFields } from "./fields/filesystem-fields";
import { ImapFields } from "./fields/imap-fields";
import { PaperlessFields } from "./fields/paperless-fields";
import { TelegramFields } from "./fields/telegram-fields";

/* ---------------- schemas ---------------- */

const commonSchema = z.object({
  name: z.string().min(1, "Name is required").max(80),
  enabled: z.boolean(),
  shared: z.boolean(),
  schedule: z.string(),
});

const filesystemConfigSchema = z.object({
  root_path: z.string().min(1, "Root path is required"),
  patterns: z.string().optional(),
  sync_since: z.string().optional(),
});

// IMAP port allows the literal strings that `<input type=number>` sometimes
// passes through; coerce handles both cases.
const imapConfigSchema = z.object({
  server: z.string().min(1, "Server is required"),
  port: z.coerce.number().int().positive().default(993),
  username: z.string().min(1, "Username is required"),
  password: z.string().min(1, "Password is required"),
  folders: z.string().optional(),
  sync_since: z.string().optional(),
});

const paperlessConfigSchema = z.object({
  url: z.string().url("Enter a valid URL"),
  token: z.string().min(1, "API token is required"),
  sync_since: z.string().optional(),
});

const telegramConfigSchema = z.object({
  api_id: z.coerce.number().int().positive("API ID required"),
  api_hash: z.string().min(1, "API hash is required"),
  phone: z.string().regex(/^\+\d{7,15}$/, "International format, e.g. +15551234567"),
  chat_filter: z.string().optional(),
  sync_since: z.string().optional(),
});

const formSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("filesystem"), config: filesystemConfigSchema, ...commonSchema.shape }),
  z.object({ type: z.literal("imap"), config: imapConfigSchema, ...commonSchema.shape }),
  z.object({ type: z.literal("paperless"), config: paperlessConfigSchema, ...commonSchema.shape }),
  z.object({ type: z.literal("telegram"), config: telegramConfigSchema, ...commonSchema.shape }),
]);

export type ConnectorFormValues = z.infer<typeof formSchema>;

/* ---------------- component ---------------- */

export interface ConnectorFormProps {
  mode: "create" | "edit";
  initial?: Partial<ConnectorFormValues>;
  onSubmit: (values: ConnectorFormValues) => Promise<void>;
  onCancel: () => void;
  submitLabel?: string;
  isAdmin?: boolean;
}

function defaultConfigFor(type: string) {
  switch (type) {
    case "filesystem":
      return { root_path: "", patterns: "*.txt,*.md" };
    case "imap":
      return { server: "", port: 993, username: "", password: "", folders: "INBOX" };
    case "paperless":
      return { url: "", token: "" };
    case "telegram":
      return { api_id: 0, api_hash: "", phone: "", chat_filter: "" };
    default:
      return {};
  }
}

function suggestedName(type: string) {
  switch (type) {
    case "filesystem":
      return "notes";
    case "imap":
      return "mailbox";
    case "paperless":
      return "paperless";
    case "telegram":
      return "telegram";
    default:
      return "";
  }
}

/**
 * Connector create/edit form. Type picker at the top dispatches into the
 * matching field group; shared fields (name, schedule, enabled, shared)
 * live in a single vertical rhythm below.
 *
 * In create mode, changing the type swaps the `config` sub-object to a
 * sensible default so the form never briefly shows validation errors for
 * fields that don't apply to the new type.
 *
 * In edit mode, the type picker is omitted (type is immutable server-
 * side) and masked secrets arrive pre-populated — the user can leave the
 * ••• in place and the backend restores the original on PUT.
 */
export function ConnectorForm({
  mode,
  initial,
  onSubmit,
  onCancel,
  submitLabel,
  isAdmin,
}: Readonly<ConnectorFormProps>) {
  const methods = useForm<ConnectorFormValues>({
    // Cast: zodResolver's inferred shape for a discriminated-union is a
    // FieldValues union that TS can't collapse to the value type. The
    // runtime behavior is correct (zod validates all branches); the
    // cast narrows the resolver's TS signature to ours.
    resolver: zodResolver(formSchema) as Resolver<ConnectorFormValues>,
    defaultValues:
      initial ??
      ({
        type: "filesystem",
        name: "",
        enabled: true,
        shared: false,
        schedule: "",
        config: { root_path: "", patterns: "*.txt,*.md" },
      } as ConnectorFormValues),
    mode: "onBlur",
  });

  const {
    control,
    getValues,
    setValue,
    register,
    handleSubmit,
    formState: { errors, isSubmitting, isDirty },
  } = methods;

  // useWatch is memoizable; `watch()` returns a non-memoizable function that
  // trips react-hooks/incompatible-library, so everywhere we actually need
  // a reactive value (not just defaults) we read via useWatch instead.
  const type = useWatch({ control, name: "type" });
  const schedule = useWatch({ control, name: "schedule" });
  const enabled = useWatch({ control, name: "enabled" });
  const shared = useWatch({ control, name: "shared" });

  useEffect(() => {
    if (mode === "edit") return;
    setValue("config", defaultConfigFor(type) as ConnectorFormValues["config"], {
      shouldValidate: false,
    });
    // getValues — not watch — so we read the current name without subscribing.
    // watch inside an effect trips react-hooks/incompatible-library because
    // RHF's watch returns a non-memoizable function.
    if (!getValues("name")) setValue("name", suggestedName(type));
    // Deliberate: only run when the user changes `type` in create mode.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [type, mode]);

  return (
    <FormProvider {...methods}>
      <form onSubmit={handleSubmit(onSubmit)} className="flex h-full min-h-0 flex-col">
        <div className="flex-1 space-y-8 overflow-y-auto px-6 py-6">
          {mode === "create" && (
            <FormSection label="Source" description="Pick the kind of data you want Nexus to index.">
              <ConnectorTypePicker
                value={type as ConnectorTypeKey}
                onChange={(v) => setValue("type", v, { shouldDirty: true })}
              />
            </FormSection>
          )}

          <FormSection
            label="Identity"
            description="A friendly name — used in search filters and sync logs."
          >
            <div className="space-y-1.5">
              <Label htmlFor="connector-name">Name</Label>
              <Input
                id="connector-name"
                {...register("name")}
                placeholder={suggestedName(type)}
                autoComplete="off"
              />
              {errors.name && <FieldError message={errors.name.message as string | undefined} />}
            </div>
          </FormSection>

          <FormSection label="Configuration">
            {type === "filesystem" && <FilesystemFields />}
            {type === "imap" && <ImapFields mode={mode} />}
            {type === "paperless" && <PaperlessFields mode={mode} />}
            {type === "telegram" && <TelegramFields mode={mode} />}
          </FormSection>

          <FormSection
            label="Sync window"
            description="How far back Nexus should pull. Leave as All history unless the source is so large the first sync becomes painful."
          >
            <SyncWindowField />
          </FormSection>

          <FormSection
            label="Schedule"
            description="When Nexus should run this connector automatically. Manual triggers always work."
          >
            <ScheduleField
              value={schedule ?? ""}
              onChange={(next) => setValue("schedule", next, { shouldDirty: true })}
            />
          </FormSection>

          <FormSection label="Visibility">
            <div className="space-y-3">
              <ToggleRow
                label="Enabled"
                hint="Disabled connectors skip scheduled syncs but stay searchable."
                checked={enabled ?? true}
                onCheckedChange={(v) => setValue("enabled", v, { shouldDirty: true })}
              />
              {isAdmin && (
                <ToggleRow
                  label="Share with all users"
                  hint="Shared connectors are visible to everyone on this Nexus instance."
                  checked={shared ?? false}
                  onCheckedChange={(v) => setValue("shared", v, { shouldDirty: true })}
                />
              )}
            </div>
          </FormSection>
        </div>

        <div className="flex items-center justify-between gap-3 border-t border-border bg-card/60 px-6 py-4 backdrop-blur">
          <p className="text-[12px] text-muted-foreground">
            {mode === "create"
              ? "Credentials are encrypted at rest with AES-256-GCM."
              : "Masked fields are preserved unless you change them."}
          </p>
          <div className="flex gap-2">
            <Button type="button" variant="ghost" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit" disabled={isSubmitting || !isDirty}>
              {isSubmitting && <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />}
              {submitLabel ?? (mode === "create" ? "Create connector" : "Save changes")}
            </Button>
          </div>
        </div>
      </form>
    </FormProvider>
  );
}
