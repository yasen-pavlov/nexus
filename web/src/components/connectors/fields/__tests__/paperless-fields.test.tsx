import { describe, expect, it } from "vitest";
import { useEffect } from "react";
import { FormProvider, useForm } from "react-hook-form";

import { render, screen } from "@/test/test-utils";
import { PaperlessFields } from "../paperless-fields";

function Harness({
  mode,
  errors,
}: Readonly<{
  mode: "create" | "edit";
  errors?: { url?: string; token?: string };
}>) {
  const methods = useForm({ defaultValues: { config: { url: "", token: "" } } });
  useEffect(() => {
    if (errors?.url) methods.setError("config.url", { message: errors.url });
    if (errors?.token)
      methods.setError("config.token", { message: errors.token });
  }, [errors, methods]);
  return (
    <FormProvider {...methods}>
      <PaperlessFields mode={mode} />
    </FormProvider>
  );
}

describe("PaperlessFields", () => {
  it("renders URL + token fields with the create-mode placeholders", () => {
    render(<Harness mode="create" />);
    expect(screen.getByLabelText("Paperless URL")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("http://paperless.home:8000"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("API token")).toBeInTheDocument();
    expect(
      screen.getByPlaceholderText("40-character token"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/Generate from Paperless-ngx/i),
    ).toBeInTheDocument();
  });

  it("uses the masked placeholder for the token in edit mode", () => {
    render(<Harness mode="edit" />);
    expect(screen.getByPlaceholderText("••••••••")).toBeInTheDocument();
  });

  it("surfaces field validation errors", async () => {
    render(
      <Harness
        mode="create"
        errors={{ url: "URL is required", token: "Token is required" }}
      />,
    );
    expect(await screen.findByText("URL is required")).toBeInTheDocument();
    expect(screen.getByText("Token is required")).toBeInTheDocument();
  });
});
