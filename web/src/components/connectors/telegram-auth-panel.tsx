import { useState } from "react";
import { Loader2, Send, ShieldCheck, Undo2 } from "lucide-react";

import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

export interface TelegramAuthPanelProps {
  phone: string;
  onStart: () => Promise<void>;
  onSubmit: (payload: { code: string; password?: string }) => Promise<void>;
  status: "idle" | "code-sent" | "authenticating" | "authenticated" | "failed";
  error?: string;
  needs2FA?: boolean;
}

/**
 * Inline two-step Telegram auth. Phone is read-only (taken from the
 * connector config), so the user only interacts with the code + optional
 * 2FA password. Success collapses the panel into the identity card on
 * the parent Identity tab.
 */
export function TelegramAuthPanel({
  phone,
  onStart,
  onSubmit,
  status,
  error,
  needs2FA,
}: Readonly<TelegramAuthPanelProps>) {
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");

  const codeSent =
    status === "code-sent" || status === "authenticating" || status === "failed";
  const busy = status === "authenticating";

  return (
    <div className="relative overflow-hidden rounded-xl border border-border bg-card">
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 opacity-70"
        style={{
          background:
            "linear-gradient(135deg, color-mix(in oklch, var(--source-telegram) 10%, transparent), transparent 60%)",
        }}
      />
      <div className="relative space-y-5 p-5">
        <header className="flex items-center gap-3">
          <div
            className="flex h-10 w-10 items-center justify-center rounded-lg"
            style={{
              backgroundColor: "color-mix(in oklch, var(--source-telegram) 16%, transparent)",
              color: "var(--source-telegram)",
            }}
          >
            <ShieldCheck className="h-5 w-5" strokeWidth={1.7} />
          </div>
          <div>
            <h3 className="text-[14px] font-medium text-foreground">
              Reconnect this Telegram account
            </h3>
            <p className="text-[12px] text-muted-foreground">
              We&apos;ll send a login code to{" "}
              <span className="font-mono text-foreground">{phone || "…"}</span>.
            </p>
          </div>
        </header>

        <ol className="flex items-center gap-2 text-[11px] font-semibold uppercase tracking-[0.08em] text-muted-foreground">
          <Step n={1} label="Send code" active={!codeSent} done={codeSent} />
          <Divider />
          <Step n={2} label="Verify" active={codeSent} />
        </ol>

        {!codeSent && (
          <div>
            <Button
              type="button"
              onClick={() => void onStart()}
              disabled={busy || !phone}
              className="gap-2"
            >
              <Send className="h-3.5 w-3.5" />
              Send code to Telegram
            </Button>
          </div>
        )}

        {codeSent && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void onSubmit({ code, password: password || undefined });
            }}
            className="space-y-4"
          >
            <div className="space-y-1.5">
              <Label htmlFor="tg-code">Login code</Label>
              <Input
                id="tg-code"
                value={code}
                onChange={(e) => setCode(e.target.value.replaceAll(/\D/g, ""))}
                inputMode="numeric"
                maxLength={6}
                placeholder="12345"
                autoFocus
                className="font-mono text-[17px] tracking-[0.3em]"
              />
            </div>
            {(needs2FA || password.length > 0) && (
              <div className="space-y-1.5">
                <Label htmlFor="tg-pw">Two-step verification password</Label>
                <Input
                  id="tg-pw"
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Only if you enabled 2FA"
                />
              </div>
            )}
            {error && (
              <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-[12.5px] text-destructive">
                {error}
              </div>
            )}
            <div className="flex items-center justify-between">
              <button
                type="button"
                className="inline-flex items-center gap-1 text-[12px] text-muted-foreground hover:text-foreground"
                onClick={() => void onStart()}
                disabled={busy}
              >
                <Undo2 className="h-3 w-3" /> Resend code
              </button>
              <Button type="submit" disabled={busy || code.length === 0}>
                {busy && <Loader2 className="mr-2 h-3.5 w-3.5 animate-spin" />}
                Verify and connect
              </Button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}

function Step({
  n,
  label,
  active,
  done,
}: Readonly<{
  n: number;
  label: string;
  active?: boolean;
  done?: boolean;
}>) {
  return (
    <li
      className={cn(
        "inline-flex items-center gap-2",
        done && "text-foreground",
        active && !done && "text-primary",
      )}
    >
      <span
        className={cn(
          "inline-flex h-5 w-5 items-center justify-center rounded-full border",
          done
            ? "border-transparent bg-primary text-primary-foreground"
            : active
              ? "border-primary text-primary"
              : "border-border text-muted-foreground",
        )}
      >
        {n}
      </span>
      {label}
    </li>
  );
}

function Divider() {
  return <li aria-hidden className="h-px w-5 bg-border" />;
}
