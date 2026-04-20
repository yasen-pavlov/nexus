import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { RefreshCcw, Undo2 } from "lucide-react";

import { Button } from "@/components/ui/button";

import { TypedConfirmDialog } from "@/components/admin/typed-confirm-dialog";

import { fetchAPI } from "@/lib/api-client";
import { useSystemStats } from "@/hooks/use-system-stats";
import { formatCount } from "@/lib/format";
import { adminKeys, syncKeys, connectorKeys } from "@/lib/query-keys";

/**
 * The big levers. Two typed-confirm actions:
 *   1. Reindex everything — recreates the OpenSearch index (if dimension
 *      changed) + clears all cursors + kicks a full sync for every
 *      connector. Measured in minutes-to-hours.
 *   2. Reset all sync cursors — forces the next sync to be full for
 *      every connector, without touching the index. Faster, less
 *      destructive, but still costs a full pass.
 */
export function MaintenanceSection() {
  const stats = useSystemStats();
  const qc = useQueryClient();
  const [confirmReindex, setConfirmReindex] = useState(false);
  const [confirmResetCursors, setConfirmResetCursors] = useState(false);

  const reindex = useMutation({
    mutationFn: () =>
      fetchAPI<{ message: string; dimension: number; connectors: number }>(
        "/api/reindex",
        { method: "POST" },
      ),
    onSuccess: (data) => {
      qc.invalidateQueries({ queryKey: adminKeys.all });
      qc.invalidateQueries({ queryKey: syncKeys.all });
      qc.invalidateQueries({ queryKey: connectorKeys.all });
      toast.success(
        `Re-index started on ${data.connectors} connector${data.connectors === 1 ? "" : "s"}`,
      );
    },
    onError: (err: Error) =>
      toast.error(err.message || "Failed to start re-index"),
  });

  const resetCursors = useMutation({
    mutationFn: async () => {
      const token = localStorage.getItem("nexus_jwt");
      const res = await fetch("/api/sync/cursors", {
        method: "DELETE",
        headers: token ? { Authorization: `Bearer ${token}` } : {},
      });
      if (res.status === 401) throw new Error("Unauthorized");
      if (!res.ok && res.status !== 204) {
        const body = await res.json().catch(() => ({ error: "" }));
        throw new Error(body.error || `HTTP ${res.status}`);
      }
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: connectorKeys.all });
      toast.success("All sync cursors cleared");
    },
    onError: (err: Error) => toast.error(err.message || "Failed"),
  });

  const totalDocs = stats.data?.total_documents ?? 0;
  const sourceCount = stats.data?.per_source.length ?? 0;
  const embeddingDim = stats.data?.embedding.dimension ?? 0;

  return (
    <div className="flex flex-col gap-3">
      <MaintenanceRow
        icon={<RefreshCcw className="size-4" aria-hidden />}
        title="Re-index everything"
        description={
          totalDocs > 0 ? (
            <>
              Recreates the OpenSearch index
              {embeddingDim > 0 ? (
                <> (dimension {embeddingDim})</>
              ) : null}
              , clears all cursors, and re-syncs every connector. Affects{" "}
              <span className="font-medium text-foreground">
                {formatCount(totalDocs)}
              </span>{" "}
              document{totalDocs === 1 ? "" : "s"} across {sourceCount}{" "}
              source{sourceCount === 1 ? "" : "s"}.
            </>
          ) : (
            "Recreates the OpenSearch index and re-syncs every connector. Index is empty today — this will be quick."
          )
        }
        onClick={() => setConfirmReindex(true)}
      />

      <MaintenanceRow
        icon={<Undo2 className="size-4" aria-hidden />}
        title="Reset all sync cursors"
        description={
          <>
            Clears the per-connector cursor so the next sync starts from the
            beginning. Cheaper than a full re-index, but still costs one
            complete pass per connector.
          </>
        }
        onClick={() => setConfirmResetCursors(true)}
      />

      <TypedConfirmDialog
        open={confirmReindex}
        onOpenChange={setConfirmReindex}
        eyebrow="Danger zone"
        title="Re-index everything?"
        body={
          <span>
            This can take minutes to hours. Search returns incomplete results
            until each connector finishes. Existing sync cursors are cleared
            — anything already indexed will be re-embedded from scratch.
          </span>
        }
        confirmPhrase="reindex everything"
        confirmLabel="Start re-index"
        variant="destructive"
        onConfirm={async () => {
          await reindex.mutateAsync();
          setConfirmReindex(false);
        }}
      />

      <TypedConfirmDialog
        open={confirmResetCursors}
        onOpenChange={setConfirmResetCursors}
        eyebrow="Danger zone"
        title="Reset all sync cursors?"
        body={
          <span>
            Next sync on each connector re-fetches everything from scratch.
            The index itself isn&apos;t recreated, so docs stay searchable
            during the pass. Still costs one full connector pass.
          </span>
        }
        confirmPhrase="reset cursors"
        confirmLabel="Clear all cursors"
        variant="destructive"
        onConfirm={async () => {
          await resetCursors.mutateAsync();
          setConfirmResetCursors(false);
        }}
      />
    </div>
  );
}

function MaintenanceRow({
  icon,
  title,
  description,
  onClick,
}: Readonly<{
  icon: React.ReactNode;
  title: string;
  description: React.ReactNode;
  onClick: () => void;
}>) {
  return (
    <div className="flex items-start gap-3 rounded-md border border-destructive/30 bg-destructive/[0.04] px-4 py-3">
      <div
        aria-hidden
        className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-md bg-destructive/10 text-destructive"
      >
        {icon}
      </div>
      <div className="flex-1 leading-[1.55]">
        <div className="text-[13.5px] font-medium text-foreground">{title}</div>
        <div className="text-[12.5px] text-muted-foreground">{description}</div>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onClick}
        className="shrink-0 border-destructive/40 text-destructive hover:bg-destructive/10"
      >
        Run
      </Button>
    </div>
  );
}
