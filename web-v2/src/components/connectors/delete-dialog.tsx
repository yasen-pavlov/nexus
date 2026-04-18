import { useState } from "react";
import { Trash2, TriangleAlert } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export interface DeleteConnectorDialogProps {
  open: boolean;
  onOpenChange: (v: boolean) => void;
  connectorName: string;
  connectorType: string;
  onConfirm: () => Promise<void>;
}

/**
 * Ceremonial, typed-confirmation delete. Removing a Telegram connector
 * nukes the MTProto session, cached avatars, and downloaded media with
 * no undo — this dialog's job is to make that cost visible up front.
 */
export function DeleteConnectorDialog({
  open,
  onOpenChange,
  connectorName,
  connectorType,
  onConfirm,
}: DeleteConnectorDialogProps) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const armed = typed === connectorName;

  const impact = impactCopy(connectorType);

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setTyped("");
        onOpenChange(next);
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader className="space-y-2">
          <div className="flex items-center gap-3">
            <div
              className="flex h-9 w-9 items-center justify-center rounded-lg"
              style={{
                backgroundColor: "color-mix(in oklch, var(--destructive) 14%, transparent)",
                color: "var(--destructive)",
              }}
            >
              <TriangleAlert className="h-4 w-4" />
            </div>
            <DialogTitle>Remove connector</DialogTitle>
          </div>
          <p className="text-[13px] text-muted-foreground">{impact}</p>
        </DialogHeader>

        <div className="space-y-2">
          <label className="text-[10px] font-semibold uppercase tracking-[0.08em] text-muted-foreground/80">
            Type <span className="font-mono text-foreground">{connectorName}</span> to confirm
          </label>
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            autoFocus
            spellCheck={false}
            placeholder={connectorName}
            className="font-mono"
          />
        </div>

        <DialogFooter className="gap-2 sm:gap-2">
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>
            Keep
          </Button>
          <Button
            variant="destructive"
            disabled={!armed || busy}
            onClick={async () => {
              setBusy(true);
              try {
                await onConfirm();
              } finally {
                setBusy(false);
              }
            }}
            className="gap-1.5"
          >
            <Trash2 className="h-3.5 w-3.5" />
            Remove {connectorName}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function impactCopy(type: string) {
  switch (type) {
    case "telegram":
      return "This destroys the MTProto session, cached avatars, and downloaded media. Search results from this connector will be removed and cannot be recovered without re-authenticating.";
    case "imap":
      return "Cached email bodies and attachment blobs will be deleted. Indexed messages are removed from search.";
    case "paperless":
      return "Indexed Paperless documents will be removed from search. Your Paperless-ngx server is untouched.";
    case "filesystem":
      return "Indexed files will be removed from search. Files on disk are untouched.";
    default:
      return "Indexed content for this connector will be removed from search.";
  }
}
