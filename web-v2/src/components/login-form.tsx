import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod/v4";
import { useNavigate } from "@tanstack/react-router";
import { useLogin, useRegister, useHealth } from "@/hooks/use-auth";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";

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
    return (
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Nexus</CardTitle>
          <CardDescription>Loading...</CardDescription>
        </CardHeader>
      </Card>
    );
  }

  // Key forces remount when isRegister changes, ensuring correct schema
  return <LoginFormInner key={isRegister ? "register" : "login"} isRegister={isRegister} />;
}

function LoginFormInner({ isRegister }: { isRegister: boolean }) {
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
        void navigate({ to: "/" });
      },
    });
  };

  return (
    <Card className="w-full max-w-sm">
      <CardHeader className="text-center">
        <CardTitle className="text-2xl">Nexus</CardTitle>
        <CardDescription>
          {isRegister
            ? "Create the first admin account"
            : "Sign in to continue"}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {isRegister && (
          <p className="mb-4 text-sm text-muted-foreground">
            No users exist yet. The first registered account will become the
            admin.
          </p>
        )}
        <form onSubmit={handleSubmit(onSubmit)} className="flex flex-col gap-4">
          <div className="flex flex-col gap-2">
            <Label htmlFor="username">Username</Label>
            <Input
              id="username"
              autoComplete="username"
              autoFocus
              {...register("username")}
            />
            {errors.username && (
              <p className="text-sm text-destructive">
                {errors.username.message}
              </p>
            )}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete={isRegister ? "new-password" : "current-password"}
              {...register("password")}
            />
            {errors.password && (
              <p className="text-sm text-destructive">
                {errors.password.message}
              </p>
            )}
          </div>
          {mutation.error && (
            <p className="text-sm text-destructive">
              {mutation.error.message}
            </p>
          )}
          <Button type="submit" className="w-full" disabled={mutation.isPending}>
            {mutation.isPending
              ? "Please wait..."
              : isRegister
                ? "Create admin account"
                : "Sign in"}
          </Button>
        </form>
      </CardContent>
    </Card>
  );
}
