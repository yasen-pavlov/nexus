import { z } from "zod/v4";
import type { SearchFilters } from "./api-types";

export const searchParamsSchema = z.object({
  q: z.string().optional(),
  sources: z.array(z.string()).optional(),
  source_names: z.array(z.string()).optional(),
  date_from: z.string().optional(),
  date_to: z.string().optional(),
});

export type SearchParams = z.infer<typeof searchParamsSchema>;

export function paramsToFilters(params: SearchParams): SearchFilters {
  return {
    sources: params.sources,
    source_names: params.source_names,
    date_from: params.date_from,
    date_to: params.date_to,
  };
}

export function hasAnyFilter(params: SearchParams): boolean {
  return (
    (params.sources?.length ?? 0) > 0 ||
    (params.source_names?.length ?? 0) > 0 ||
    !!params.date_from ||
    !!params.date_to
  );
}
