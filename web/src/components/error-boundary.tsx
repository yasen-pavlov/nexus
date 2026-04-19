import { Component, type ErrorInfo, type ReactNode } from "react";

import { ErrorPage } from "./error-page";

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Last-resort React error boundary. TanStack Router's defaultErrorComponent
 * catches loader errors and most render errors inside route components, but
 * a few code paths (event handlers, async effects that re-throw) escape it.
 * This boundary wraps the main scroll area so the AppShell never goes blank.
 *
 * The parent passes `key={currentPath}` so React tears the boundary down on
 * route change, automatically clearing any caught error from the previous
 * route.
 */
export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // Surface to the console — the technical-details disclosure on the
    // fallback page exposes error.message but the stack lives here for
    // anyone with devtools open.
    // eslint-disable-next-line no-console
    console.error("App error boundary caught:", error, info);
  }

  render() {
    if (this.state.error) {
      return (
        <ErrorPage
          kind="error"
          error={this.state.error}
          onReload={() => window.location.reload()}
        />
      );
    }
    return this.props.children;
  }
}
