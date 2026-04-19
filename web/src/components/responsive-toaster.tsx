import { Toaster } from "@/components/ui/sonner";
import { useIsMobile } from "@/hooks/use-mobile";

// Mobile keyboards eat the bottom of the viewport — top-center keeps toasts
// from being hidden behind the on-screen keyboard or browser chrome.
export function ResponsiveToaster() {
  const isMobile = useIsMobile();
  return (
    <Toaster richColors position={isMobile ? "top-center" : "bottom-right"} />
  );
}
