import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { FormProvider, useForm } from "react-hook-form";
import { http, HttpResponse } from "msw";

import { render, screen, userEvent, waitFor } from "@/test/test-utils";
import { setToken } from "@/lib/api-client";
import { fakeToken } from "@/test/mocks/handlers";
import { server } from "@/test/mocks/server";
import { ICalFields } from "../ical-fields";

function Harness({
  mode = "create",
  config = { username: "", password: "", calendars: [] as string[] },
}: Readonly<{
  mode?: "create" | "edit";
  config?: Record<string, unknown>;
}>) {
  const methods = useForm({ defaultValues: { type: "ical", config } });
  return (
    <FormProvider {...methods}>
      <ICalFields mode={mode} />
    </FormProvider>
  );
}

beforeEach(() => setToken(fakeToken));
afterEach(() => server.resetHandlers());

describe("ICalFields", () => {
  it("renders credential fields and disables discover until creds are entered", () => {
    render(<Harness />);
    expect(screen.getByLabelText("Apple ID")).toBeInTheDocument();
    expect(screen.getByLabelText("App-specific password")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /discover calendars/i }),
    ).toBeDisabled();
  });

  it("discovers calendars and toggles selection", async () => {
    server.use(
      http.post("*/api/connectors/discover", () =>
        HttpResponse.json({
          data: [
            { id: "/u/cal/home/", name: "Home" },
            { id: "/u/cal/work/", name: "Work" },
          ],
        }),
      ),
    );
    render(<Harness />);

    await userEvent.type(screen.getByLabelText("Apple ID"), "me@icloud.com");
    await userEvent.type(
      screen.getByLabelText("App-specific password"),
      "abcd-efgh-ijkl-mnop",
    );

    const btn = screen.getByRole("button", { name: /discover calendars/i });
    await waitFor(() => expect(btn).toBeEnabled());
    await userEvent.click(btn);

    const work = await screen.findByText("Work");
    expect(screen.getByText("Home")).toBeInTheDocument();
    // Toggling a calendar marks its row pressed.
    const workRow = work.closest("button")!;
    expect(workRow).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(workRow);
    expect(workRow).toHaveAttribute("aria-pressed", "true");
  });

  it("surfaces a discovery error", async () => {
    server.use(
      http.post("*/api/connectors/discover", () =>
        HttpResponse.json(
          { error: "authentication failed — check the Apple ID" },
          { status: 502 },
        ),
      ),
    );
    render(<Harness />);
    await userEvent.type(screen.getByLabelText("Apple ID"), "me@icloud.com");
    await userEvent.type(
      screen.getByLabelText("App-specific password"),
      "bad-password",
    );
    await userEvent.click(
      screen.getByRole("button", { name: /discover calendars/i }),
    );
    expect(
      await screen.findByText(/authentication failed/i),
    ).toBeInTheDocument();
  });

  it("shows the current selection count in edit mode before re-discovering", () => {
    render(
      <Harness
        mode="edit"
        config={{
          username: "me@icloud.com",
          password: "",
          calendars: ["/u/cal/home/", "/u/cal/work/"],
        }}
      />,
    );
    expect(screen.getByText(/2 calendars selected/i)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("••••••••")).toBeInTheDocument();
  });
});
