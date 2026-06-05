import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  Check,
  Copy,
  KeyRound,
  Plus,
  ShieldAlert,
  Terminal,
  Trash2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { TypedConfirmDialog } from "@/components/admin/typed-confirm-dialog";
import { useApiTokens } from "@/hooks/use-api-tokens";
import type { ApiToken } from "@/lib/api-types";
import {
  formatAbsolute,
  formatRelative,
  futureISO,
  isExpired,
} from "@/lib/format";

/**
 * API tokens — self-service personal access tokens for agents/CLIs that call
 * Nexus on your behalf. A token acts as you, so treat it like a password.
 * Lives on /account because it's per-user (not admin-gated).
 */
export function ApiTokensSection() {
  const { data, isPending, create, revoke } = useApiTokens();
  const [createOpen, setCreateOpen] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState<ApiToken | null>(null);
  const tokens = data ?? [];

  return (
    <section className="mt-6 overflow-hidden rounded-lg border border-border bg-card">
      <header className="flex items-start gap-3 p-5">
        <span
          aria-hidden
          className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary"
        >
          <Terminal className="size-4" />
        </span>
        <div className="min-w-0 flex-1 leading-tight">
          <h2 className="text-[15px] font-medium tracking-[-0.005em] text-foreground">
            API tokens
          </h2>
          <p className="mt-1 text-[12.5px] leading-[1.55] text-muted-foreground">
            Long-lived tokens for agents and scripts that call Nexus as you.
            Send them as{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[11.5px] text-foreground">
              Authorization: Bearer …
            </code>
            . Treat each like a password.
          </p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="shrink-0 gap-1.5"
          onClick={() => setCreateOpen(true)}
        >
          <Plus className="size-3.5" aria-hidden />
          New token
        </Button>
      </header>

      <div className="border-t border-border/70">
        {isPending ? (
          <TokenListSkeleton />
        ) : tokens.length === 0 ? (
          <EmptyState />
        ) : (
          <ul>
            {tokens.map((tok) => (
              <TokenRow
                key={tok.id}
                token={tok}
                onRevoke={() => setRevokeTarget(tok)}
              />
            ))}
          </ul>
        )}
      </div>

      {createOpen && (
        <NewTokenSheet
          open={createOpen}
          onOpenChange={setCreateOpen}
          onCreate={(args) => create.mutateAsync(args)}
          isPending={create.isPending}
        />
      )}

      {revokeTarget && (
        <TypedConfirmDialog
          open={revokeTarget !== null}
          onOpenChange={(v) => {
            if (!v) setRevokeTarget(null);
          }}
          title="Revoke this token"
          eyebrow="Danger zone"
          icon={ShieldAlert}
          body={
            <>
              Any agent or script using{" "}
              <span className="font-medium text-foreground">
                {revokeTarget.name}
              </span>{" "}
              will stop working immediately. This can't be undone.
            </>
          }
          confirmPhrase={revokeTarget.name}
          confirmLabel="Revoke token"
          onConfirm={async () => {
            await revoke.mutateAsync(revokeTarget.id);
            setRevokeTarget(null);
          }}
        />
      )}
    </section>
  );
}

function TokenRow({
  token,
  onRevoke,
}: Readonly<{ token: ApiToken; onRevoke: () => void }>) {
  const expired = isExpired(token.expires_at);

  return (
    <li className="flex items-center gap-3 border-t border-border/40 px-5 py-3.5 first:border-t-0">
      <span
        aria-hidden
        className="flex size-8 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground"
      >
        <KeyRound className="size-4" />
      </span>
      <div className="min-w-0 flex-1 leading-tight">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-[13.5px] font-medium text-foreground">
            {token.name}
          </span>
          {expired && (
            <span className="rounded bg-destructive/10 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-[0.08em] text-destructive">
              expired
            </span>
          )}
        </div>
        <div className="mt-0.5 text-[12px] text-muted-foreground">
          <span title={formatAbsolute(token.created_at)}>
            created {formatRelative(token.created_at)}
          </span>
          {" · "}
          <span title={formatAbsolute(token.last_used_at)}>
            {token.last_used_at
              ? `last used ${formatRelative(token.last_used_at)}`
              : "never used"}
          </span>
          {token.expires_at ? (
            <span title={formatAbsolute(token.expires_at)}>
              {" · "}
              {expired ? "expired " : "expires "}
              {formatRelative(token.expires_at)}
            </span>
          ) : (
            <span>{" · "}no expiry</span>
          )}
        </div>
      </div>
      <Button
        type="button"
        size="icon-sm"
        variant="ghost"
        aria-label={`Revoke ${token.name}`}
        className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
        onClick={onRevoke}
      >
        <Trash2 className="size-4" aria-hidden />
      </Button>
    </li>
  );
}

function EmptyState() {
  return (
    <div className="px-5 py-8 text-center">
      <p className="text-[13px] text-muted-foreground">
        No tokens yet. Create one to let an agent or script query Nexus on your
        behalf.
      </p>
    </div>
  );
}

function TokenListSkeleton() {
  return (
    <div className="flex flex-col gap-3 p-5">
      {[0, 1].map((i) => (
        <div key={i} className="flex items-center gap-3">
          <Skeleton className="size-8 rounded-md" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-3.5 w-32" />
            <Skeleton className="h-3 w-48" />
          </div>
        </div>
      ))}
    </div>
  );
}

const EXPIRY_OPTIONS: ReadonlyArray<{ value: string; label: string; days: number }> = [
  { value: "never", label: "Never", days: 0 },
  { value: "30", label: "30 days", days: 30 },
  { value: "90", label: "90 days", days: 90 },
  { value: "365", label: "1 year", days: 365 },
];

const schema = z.object({
  name: z.string().trim().min(1, "Name required").max(100, "Max 100 characters"),
});
type Values = z.infer<typeof schema>;

function NewTokenSheet({
  open,
  onOpenChange,
  onCreate,
  isPending,
}: Readonly<{
  open: boolean;
  onOpenChange: (v: boolean) => void;
  onCreate: (args: { name: string; expires_at?: string }) => Promise<{
    token: string;
  }>;
  isPending: boolean;
}>) {
  const [secret, setSecret] = useState<string | null>(null);
  const [expiry, setExpiry] = useState("never");
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { name: "" },
  });

  const reset = () => {
    form.reset({ name: "" });
    setExpiry("never");
    setSecret(null);
  };

  const submit = form.handleSubmit(async ({ name }) => {
    const opt = EXPIRY_OPTIONS.find((o) => o.value === expiry);
    const res = await onCreate({ name, expires_at: futureISO(opt?.days ?? 0) });
    if (res?.token) setSecret(res.token);
  });

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) reset();
      }}
    >
      <SheetContent side="right" className="flex w-full flex-col p-0 sm:max-w-md">
        <SheetHeader className="border-b border-border px-6 py-4">
          <div className="flex items-center gap-2.5">
            <div
              aria-hidden
              className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary"
            >
              <Terminal className="size-4" />
            </div>
            <div className="min-w-0">
              <SheetTitle className="text-[15px] font-medium">
                {secret ? "Token created" : "Create API token"}
              </SheetTitle>
              <SheetDescription className="text-[12px] text-muted-foreground">
                {secret
                  ? "Copy it now — you won't be able to see it again."
                  : "Name it for the agent or script that will use it."}
              </SheetDescription>
            </div>
          </div>
        </SheetHeader>

        {secret ? (
          <SecretReveal secret={secret} onDone={() => onOpenChange(false)} />
        ) : (
          <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
            <div className="flex-1 overflow-y-auto px-6 py-5">
              <div className="flex flex-col gap-5">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="tok-name" className="text-[13px] font-medium">
                    Token name
                  </Label>
                  <Input
                    id="tok-name"
                    {...form.register("name")}
                    autoFocus
                    autoComplete="off"
                    spellCheck={false}
                    placeholder="e.g. claude-code agent"
                    className="h-10 text-[13px]"
                  />
                  {form.formState.errors.name?.message && (
                    <p className="text-[12px] leading-[1.5] text-destructive">
                      {form.formState.errors.name.message}
                    </p>
                  )}
                </div>

                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="tok-expiry" className="text-[13px] font-medium">
                    Expiry
                  </Label>
                  <Select
                    value={expiry}
                    onValueChange={(v) => setExpiry(v ?? "never")}
                  >
                    <SelectTrigger id="tok-expiry" className="h-10">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {EXPIRY_OPTIONS.map((o) => (
                        <SelectItem key={o.value} value={o.value}>
                          {o.label}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <p className="text-[12px] leading-[1.5] text-muted-foreground">
                    A non-expiring token is convenient for an always-on agent —
                    revoke it here when you're done.
                  </p>
                </div>
              </div>
            </div>

            <div className="flex justify-end gap-2 border-t border-border/70 bg-background/95 px-6 py-3 backdrop-blur">
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => onOpenChange(false)}
                disabled={isPending}
              >
                Cancel
              </Button>
              <Button type="submit" size="sm" disabled={isPending}>
                {isPending ? "Creating…" : "Create token"}
              </Button>
            </div>
          </form>
        )}
      </SheetContent>
    </Sheet>
  );
}

function SecretReveal({
  secret,
  onDone,
}: Readonly<{ secret: string; onDone: () => void }>) {
  const [copied, setCopied] = useState(false);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      globalThis.setTimeout(() => setCopied(false), 1500);
    } catch {
      // navigator.clipboard is unavailable on insecure (http) origins — the
      // value is selectable in the box, so this just no-ops.
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex-1 overflow-y-auto px-6 py-5">
        <div className="flex flex-col gap-3">
          <div className="flex items-stretch gap-2 rounded-md border border-border bg-muted/30 p-2">
            <code className="flex-1 break-all px-1 py-1 font-mono text-[12.5px] leading-[1.5] text-foreground">
              {secret}
            </code>
            <Button
              type="button"
              variant="outline"
              size="sm"
              // Fixed width so the Copy→Copied label/icon swap doesn't resize
              // the button and reflow the token text beside it.
              className="w-[5.25rem] shrink-0 justify-center gap-1.5 self-start"
              onClick={copy}
            >
              {copied ? (
                <Check className="size-3.5" aria-hidden />
              ) : (
                <Copy className="size-3.5" aria-hidden />
              )}
              {copied ? "Copied" : "Copy"}
            </Button>
          </div>
          <div className="flex items-start gap-2.5 rounded-md border border-destructive/25 bg-destructive/5 p-3 text-[13px]">
            <ShieldAlert
              className="mt-0.5 size-3.5 shrink-0 text-destructive"
              aria-hidden
            />
            <span className="flex-1 leading-[1.55] text-muted-foreground">
              This is the only time the token is shown. Store it somewhere safe
              now — if you lose it, revoke it and create a new one.
            </span>
          </div>
        </div>
      </div>
      <div className="flex justify-end gap-2 border-t border-border/70 bg-background/95 px-6 py-3 backdrop-blur">
        <Button type="button" size="sm" onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}
