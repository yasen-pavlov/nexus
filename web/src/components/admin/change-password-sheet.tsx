import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Eye, EyeOff, KeyRound, Sparkles } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";

import { useUsers } from "@/hooks/use-users";

export interface ChangePasswordSheetProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  userId: string;
  label: string;
  onDone?: () => void;
}

const schema = z.object({
  password: z.string().min(8, "At least 8 characters"),
});
type Values = z.infer<typeof schema>;

export function ChangePasswordSheet({
  open,
  onOpenChange,
  userId,
  label,
  onDone,
}: Readonly<ChangePasswordSheetProps>) {
  const { changePassword } = useUsers();
  const [showPassword, setShowPassword] = useState(false);
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { password: "" },
  });

  const submit = form.handleSubmit(async ({ password }) => {
    await changePassword.mutateAsync({ userId, password });
    form.reset({ password: "" });
    setShowPassword(false);
    onOpenChange(false);
    onDone?.();
  });

  return (
    <Sheet
      open={open}
      onOpenChange={(v) => {
        onOpenChange(v);
        if (!v) {
          form.reset({ password: "" });
          setShowPassword(false);
        }
      }}
    >
      <SheetContent
        side="right"
        className="flex w-full flex-col p-0 sm:max-w-md"
      >
        <SheetHeader className="border-b border-border px-6 py-4">
          <div className="flex items-center gap-2.5">
            <div
              aria-hidden
              className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/15 text-primary"
            >
              <KeyRound className="size-4" />
            </div>
            <div className="min-w-0">
              <SheetTitle className="text-[15px] font-medium">
                Change password
              </SheetTitle>
              <SheetDescription className="text-[12px] text-muted-foreground">
                Set a new password for {label}.
              </SheetDescription>
            </div>
          </div>
        </SheetHeader>

        <form onSubmit={submit} className="flex min-h-0 flex-1 flex-col">
          <div className="flex-1 overflow-y-auto px-6 py-5">
            <div className="flex flex-col gap-5">
              <div className="flex flex-col gap-1.5">
                <Label
                  htmlFor="cp-password"
                  className="text-[13px] font-medium"
                >
                  New password
                </Label>
                <div className="relative">
                  <Input
                    id="cp-password"
                    {...form.register("password")}
                    type={showPassword ? "text" : "password"}
                    autoComplete="new-password"
                    autoFocus
                    className="h-10 pr-10 font-mono text-[13px]"
                    placeholder="min 8 characters"
                  />
                  <button
                    type="button"
                    onClick={() => setShowPassword((v) => !v)}
                    aria-label={showPassword ? "Hide password" : "Show password"}
                    className="absolute right-1.5 top-1/2 flex size-7 -translate-y-1/2 items-center justify-center rounded text-muted-foreground/70 transition-colors hover:text-foreground"
                    tabIndex={-1}
                  >
                    {showPassword ? (
                      <EyeOff className="size-3.5" aria-hidden />
                    ) : (
                      <Eye className="size-3.5" aria-hidden />
                    )}
                  </button>
                </div>
                {form.formState.errors.password?.message && (
                  <p className="text-[12px] leading-[1.5] text-destructive">
                    {form.formState.errors.password.message}
                  </p>
                )}
              </div>

              <div className="flex items-start gap-2.5 rounded-md border border-primary/25 bg-primary/5 p-3 text-[13px]">
                <Sparkles
                  className="mt-0.5 size-3.5 shrink-0 text-primary"
                  aria-hidden
                />
                <span className="flex-1 leading-[1.55] text-muted-foreground">
                  You stay signed in after saving — rotate freely.
                </span>
              </div>
            </div>
          </div>

          <div className="flex justify-end gap-2 border-t border-border/70 bg-background/95 px-6 py-3 backdrop-blur">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={changePassword.isPending}
            >
              Cancel
            </Button>
            <Button type="submit" size="sm" disabled={changePassword.isPending}>
              {changePassword.isPending ? "Saving…" : "Save password"}
            </Button>
          </div>
        </form>
      </SheetContent>
    </Sheet>
  );
}
