import { useMutation } from "@tanstack/react-query";
import { getToken, clearToken } from "@/lib/api-client";

interface DownloadArgs {
  id: string;
  suggestedFilename?: string;
}

// useDocumentDownload can't go through fetchAPI because the response is a
// binary blob, not the usual {data, error} JSON envelope. Fetch raw, honour
// Content-Disposition when present, and trigger a browser download via an
// off-DOM <a download> click.
export function useDocumentDownload() {
  return useMutation({
    mutationFn: async ({ id, suggestedFilename }: DownloadArgs) => {
      const headers = new Headers();
      const token = getToken();
      if (token) headers.set("Authorization", `Bearer ${token}`);
      const res = await fetch(`/api/documents/${id}/content?download=1`, { headers });
      if (res.status === 401) {
        clearToken();
        throw new Error("Unauthorized");
      }
      if (!res.ok) {
        let message = "Download failed";
        try {
          const body = await res.json();
          if (body.error) message = body.error;
        } catch {
          // Response body wasn't JSON — keep the default message.
        }
        throw new Error(message);
      }

      let filename = suggestedFilename ?? "download";
      const disposition = res.headers.get("Content-Disposition") ?? "";
      const match = disposition.match(/filename="?([^";]+)"?/);
      if (match) filename = match[1];

      const blob = await res.blob();
      const blobURL = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = blobURL;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(blobURL);
    },
  });
}
