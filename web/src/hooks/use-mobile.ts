import * as React from "react"

const MOBILE_BREAKPOINT = 768

function readIsMobile(): boolean {
  if (typeof globalThis.window === "undefined") return false
  return globalThis.innerWidth < MOBILE_BREAKPOINT
}

export function useIsMobile() {
  // Lazy initializer seeds from the real viewport on first paint — no
  // useEffect(setState) dance that the React Compiler rules flag.
  const [isMobile, setIsMobile] = React.useState<boolean>(readIsMobile)

  React.useEffect(() => {
    const mql = globalThis.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const onChange = () => setIsMobile(readIsMobile())
    mql.addEventListener("change", onChange)
    return () => mql.removeEventListener("change", onChange)
  }, [])

  return isMobile
}
