import { describe, it, expect, vi } from "vitest";
import { render, screen, userEvent } from "@/test/test-utils";
import { AttachmentChip } from "../attachment-chip";

describe("AttachmentChip", () => {
  it("renders the filename and a formatted size", () => {
    render(
      <AttachmentChip
        filename="receipt.pdf"
        size={2048}
        onDownload={() => undefined}
      />,
    );
    expect(screen.getByText("receipt.pdf")).toBeInTheDocument();
    expect(screen.getByText(/2\.0 KB/)).toBeInTheDocument();
  });

  it("omits the size label when size is zero or missing", () => {
    render(
      <AttachmentChip filename="thing" onDownload={() => undefined} />,
    );
    expect(screen.queryByText(/KB|MB|GB|B$/)).not.toBeInTheDocument();
  });

  it("invokes onDownload when clicked", async () => {
    const onDownload = vi.fn();
    render(
      <AttachmentChip
        filename="a.pdf"
        size={100}
        onDownload={onDownload}
      />,
    );
    await userEvent.click(screen.getByRole("button"));
    expect(onDownload).toHaveBeenCalledOnce();
  });
});
