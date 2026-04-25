import { createFileRoute } from "@tanstack/react-router";
import { z } from "zod/v4";

import { AskLanding } from "@/components/ask/ask-landing";

const askSearchSchema = z.object({
  // Carried over from the search-bar's Ask-mode handoff. The landing
  // page sees this, creates a chat, and fires the first message.
  q: z.string().optional(),
});

export const Route = createFileRoute("/_authenticated/ask/")({
  validateSearch: askSearchSchema,
  component: AskPage,
});

function AskPage() {
  const { q } = Route.useSearch();
  return <AskLanding initialQuery={q} />;
}
