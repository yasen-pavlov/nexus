import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";

import { fetchAPI } from "@/lib/api-client";
import { connectorKeys, identityKeys } from "@/lib/query-keys";

export type TelegramAuthPhase =
  | "idle"
  | "code-sent"
  | "authenticating"
  | "authenticated"
  | "failed";

/**
 * Two-step Telegram auth flow:
 *   (1) start   → backend sends the user a code via MTProto
 *   (2) submit  → user enters code (+ optional 2FA password)
 *   on success  → identity refetch invalidates the "authenticated as X" badge
 *
 * Phase state lives inside the hook so the Identity tab can drive its
 * three-state UI (resend / verify / success) without threading booleans
 * from the parent.
 */
export function useTelegramAuth(connectorId: string) {
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<TelegramAuthPhase>("idle");
  const [error, setError] = useState<string | undefined>(undefined);
  const [needs2FA, setNeeds2FA] = useState(false);

  const start = useMutation({
    mutationFn: () =>
      fetchAPI<{ status: string; message?: string }>(
        `/api/connectors/${connectorId}/auth/start`,
        { method: "POST", headers: { "Content-Type": "application/json" }, body: "{}" },
      ),
    onMutate: () => {
      setError(undefined);
    },
    onSuccess: () => {
      setPhase("code-sent");
      toast.success("Code sent — check your Telegram app.");
    },
    onError: (err) => {
      const msg = err instanceof Error ? err.message : "Failed to start Telegram auth";
      setPhase("failed");
      setError(msg);
      toast.error("Couldn't send code", { description: msg });
    },
  });

  const submit = useMutation({
    mutationFn: (payload: { code: string; password?: string }) =>
      fetchAPI<{ status: string }>(`/api/connectors/${connectorId}/auth/code`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      }),
    onMutate: () => {
      setPhase("authenticating");
      setError(undefined);
    },
    onSuccess: () => {
      setPhase("authenticated");
      // The successful /auth/code call updates external_id/name on the
      // connector config server-side — refresh both the connector list
      // (for identity badges) and the /me/identities cache.
      queryClient.invalidateQueries({ queryKey: connectorKeys.all });
      queryClient.invalidateQueries({ queryKey: identityKeys.all });
    },
    onError: (err) => {
      const msg = err instanceof Error ? err.message : "Verification failed";
      setPhase("failed");
      setError(msg);
      // If the backend complains about 2FA, expose the password field next render.
      if (/2fa|password/i.test(msg)) setNeeds2FA(true);
    },
  });

  return {
    phase,
    error,
    needs2FA,
    // Swallow mutation rejections at the call-site boundary — onError
    // already drove state + toast. Re-throwing here would produce an
    // unhandled-promise warning every time a user hits the wrong code.
    start: async () => {
      try {
        await start.mutateAsync();
      } catch {
        /* handled in onError */
      }
    },
    submit: async (payload: { code: string; password?: string }) => {
      try {
        await submit.mutateAsync(payload);
      } catch {
        /* handled in onError */
      }
    },
    reset: () => {
      setPhase("idle");
      setError(undefined);
      setNeeds2FA(false);
    },
  };
}
