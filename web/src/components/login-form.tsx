import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod/v4";
import { useNavigate } from "@tanstack/react-router";
import { Library, Sparkles } from "lucide-react";
import { useHealth, useLogin, useRegister } from "@/hooks/use-auth";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

const loginSchema = z.object({
  username: z.string().min(1, "Username is required"),
  password: z.string().min(1, "Password is required"),
});

const registerSchema = z.object({
  username: z.string().min(1, "Username is required"),
  password: z.string().min(8, "Password must be at least 8 characters"),
});

type FormValues = z.infer<typeof loginSchema>;

export function LoginForm() {
  const { data: health, isLoading } = useHealth();
  const isRegister = health?.setup_required === true;

  if (isLoading) {
    return <div className="text-[13px] text-muted-foreground">Loading…</div>;
  }

  return (
    <LoginFormInner
      key={isRegister ? "register" : "login"}
      isRegister={isRegister}
    />
  );
}

function LoginFormInner({ isRegister }: Readonly<{ isRegister: boolean }>) {
  const navigate = useNavigate();
  const loginMutation = useLogin();
  const registerMutation = useRegister();
  const mutation = isRegister ? registerMutation : loginMutation;

  const {
    register,
    handleSubmit,
    formState: { errors },
  } = useForm<FormValues>({
    resolver: zodResolver(isRegister ? registerSchema : loginSchema),
    defaultValues: { username: "", password: "" },
  });

  const onSubmit = (values: FormValues) => {
    mutation.mutate(values, {
      onSuccess: () => {
        navigate({ to: "/" });
      },
    });
  };

  return (
    <div className="w-full max-w-[380px] px-4">
      <div className="mb-8 flex items-center gap-3">
        <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
          <Library className="size-5" aria-hidden strokeWidth={2.25} />
        </div>
        <div className="leading-tight">
          <div className="text-[17px] font-semibold tracking-[-0.01em]">
            Nexus
          </div>
          <div className="text-[12px] text-muted-foreground">
            personal search
          </div>
        </div>
      </div>

      {isRegister && (
        <div
          role="status"
          className="mb-6 flex items-start gap-2.5 rounded-lg border border-primary/25 bg-primary/5 px-3 py-2.5 text-[13px]"
        >
          <Sparkles
            className="mt-0.5 size-4 shrink-0 text-primary"
            aria-hidden
          />
          <div className="leading-snug">
            <div className="font-medium text-foreground">First boot</div>
            <div className="text-muted-foreground">
              No operators exist yet. The account you create now becomes the
              admin for this Nexus.
            </div>
          </div>
        </div>
      )}

      <div className="mb-5">
        <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.01em]">
          {isRegister ? "Welcome to your Nexus" : "Sign in"}
        </h1>
        <p className="mt-1 text-[13.5px] text-muted-foreground">
          {isRegister
            ? "Pick a username and password you'll remember."
            : "Access your personal search."}
        </p>
      </div>

      <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="username" className="text-[12px] font-medium">
            Username
          </Label>
          <Input
            id="username"
            autoComplete="username"
            autoFocus
            className="h-11 text-[15px]"
            {...register("username")}
          />
          {errors.username && (
            <p className="text-[12px] text-destructive">
              {errors.username.message}
            </p>
          )}
        </div>

        <div className="flex flex-col gap-1.5">
          <Label htmlFor="password" className="text-[12px] font-medium">
            Password
          </Label>
          <Input
            id="password"
            type="password"
            autoComplete={isRegister ? "new-password" : "current-password"}
            className="h-11 text-[15px]"
            {...register("password")}
          />
          {errors.password && (
            <p className="text-[12px] text-destructive">
              {errors.password.message}
            </p>
          )}
        </div>

        {mutation.error && (
          <div
            role="alert"
            className="rounded-md border border-destructive/30 bg-destructive/5 px-3 py-2 text-[13px] text-destructive"
          >
            {mutation.error.message}
          </div>
        )}

        <Button
          type="submit"
          disabled={mutation.isPending}
          className="mt-1 h-11 w-full text-[14px] font-medium"
        >
          {submitLabel(mutation.isPending, isRegister)}
        </Button>
      </form>

      <p className="mt-8 text-center text-[11.5px] text-muted-foreground/80">
        {isRegister
          ? "Additional operators can be added later from Users."
          : "Running on your machine. Your data stays with you."}
      </p>
    </div>
  );
}

function submitLabel(isPending: boolean, isRegister: boolean): string {
  if (isPending) return "Please wait…";
  return isRegister ? "Create admin account" : "Sign in";
}
