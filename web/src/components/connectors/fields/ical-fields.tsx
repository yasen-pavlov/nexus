import { useState } from "react";
import {
  useFormContext,
  useWatch,
  type FieldErrors,
  type FieldError as RHFFieldError,
} from "react-hook-form";
import { CalendarDays, Check, Loader2, RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { FieldError, SecretInput } from "../form-primitives";
import { fetchAPI } from "@/lib/api-client";
import type { DiscoveredResource } from "@/lib/api-types";
import { cn } from "@/lib/utils";

type ConfigErrors =
  | {
      username?: RHFFieldError;
      password?: RHFFieldError;
      calendars?: RHFFieldError;
    }
  | undefined;

/**
 * iCloud calendar (CalDAV) connector fields. Credentials drive a discovery
 * call that lists the account's calendars; the user ticks which to sync. The
 * selection is stored in config.calendars (an array of calendar paths).
 */
export function ICalFields({ mode }: Readonly<{ mode: "create" | "edit" }>) {
  const { register, control, setValue, getValues, formState } =
    useFormContext();
  const errors = (formState.errors as FieldErrors).config as ConfigErrors;

  const selected =
    (useWatch({ control, name: "config.calendars" }) as string[] | undefined) ??
    [];
  const username = useWatch({ control, name: "config.username" }) as
    | string
    | undefined;
  const password = useWatch({ control, name: "config.password" }) as
    | string
    | undefined;

  const [discovered, setDiscovered] = useState<DiscoveredResource[] | null>(
    null,
  );
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const canDiscover = Boolean(username) && Boolean(password) && !loading;

  const discover = async () => {
    setLoading(true);
    setError(null);
    try {
      const cfg = getValues("config") as {
        username?: string;
        password?: string;
        endpoint?: string;
      };
      const resources = await fetchAPI<DiscoveredResource[]>(
        "/api/connectors/discover",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            type: "ical",
            config: {
              username: cfg.username,
              password: cfg.password,
              endpoint: cfg.endpoint,
            },
          }),
        },
      );
      setDiscovered(resources);
    } catch (e) {
      setError((e as Error).message || "Discovery failed");
    } finally {
      setLoading(false);
    }
  };

  const toggle = (id: string) => {
    const next = selected.includes(id)
      ? selected.filter((x) => x !== id)
      : [...selected, id];
    setValue("config.calendars", next, { shouldDirty: true });
  };

  // Hint shown before the user has discovered calendars — flattened out of the
  // JSX to avoid a nested ternary.
  const emptyHint =
    mode === "edit"
      ? "Re-enter your app-specific password, then discover to choose calendars."
      : "Enter your Apple ID and password, then discover to choose which calendars to sync.";
  const selectionHint =
    selected.length > 0 ? (
      <p className="text-[12px] text-muted-foreground">
        {selected.length} calendar{selected.length === 1 ? "" : "s"} selected.
        Re-discover to change.
      </p>
    ) : (
      <p className="text-[12px] leading-[1.5] text-muted-foreground">
        {emptyHint}
      </p>
    );

  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="ical-user">Apple ID</Label>
        <Input
          id="ical-user"
          autoComplete="off"
          placeholder="you@icloud.com"
          {...register("config.username")}
        />
        <FieldError message={errors?.username?.message as string | undefined} />
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="ical-pw">App-specific password</Label>
        <SecretInput
          id="ical-pw"
          {...register("config.password")}
          maskedPlaceholder={mode === "edit" ? "••••••••" : "xxxx-xxxx-xxxx-xxxx"}
        />
        <FieldError message={errors?.password?.message as string | undefined} />
        <p className="text-[12px] leading-[1.5] text-muted-foreground">
          Generate a <em>dedicated</em> app-specific password at appleid.apple.com
          → Sign-In and Security. Keeping it separate from your mail password
          lets you revoke calendar access on its own.
        </p>
      </div>

      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <Label>Calendars</Label>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="gap-1.5"
            disabled={!canDiscover}
            onClick={() => void discover()}
          >
            {loading ? (
              <Loader2 className="size-3.5 animate-spin" aria-hidden />
            ) : (
              <RefreshCw className="size-3.5" aria-hidden />
            )}
            {discovered ? "Re-discover" : "Discover calendars"}
          </Button>
        </div>

        {error && <FieldError message={error} />}

        {discovered ? (
          <CalendarPicker
            calendars={discovered}
            selected={selected}
            onToggle={toggle}
          />
        ) : (
          selectionHint
        )}
      </div>
    </div>
  );
}

function CalendarPicker({
  calendars,
  selected,
  onToggle,
}: Readonly<{
  calendars: DiscoveredResource[];
  selected: string[];
  onToggle: (id: string) => void;
}>) {
  if (calendars.length === 0) {
    return (
      <p className="text-[12px] text-muted-foreground">
        No event calendars found on this account.
      </p>
    );
  }
  return (
    <ul className="divide-y divide-border/60 overflow-hidden rounded-md border border-border">
      {calendars.map((cal) => {
        const on = selected.includes(cal.id);
        return (
          <li key={cal.id}>
            <button
              type="button"
              onClick={() => onToggle(cal.id)}
              aria-pressed={on}
              className={cn(
                "flex w-full items-center gap-3 px-3 py-2 text-left text-[13px] transition-colors",
                on ? "bg-primary/5" : "hover:bg-muted/40",
              )}
            >
              <span
                aria-hidden
                className={cn(
                  "flex size-4 shrink-0 items-center justify-center rounded border",
                  on
                    ? "border-primary bg-primary text-primary-foreground"
                    : "border-border",
                )}
              >
                {on && <Check className="size-3" />}
              </span>
              <CalendarDays
                className="size-4 shrink-0 text-muted-foreground"
                aria-hidden
              />
              <span className="flex-1 truncate text-foreground">{cal.name}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
