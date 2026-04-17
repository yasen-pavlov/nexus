import { test, expect } from "@playwright/test";

test("redirects to login when not authenticated", async ({ page }) => {
  await page.goto("/");
  // Should redirect to /login
  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByText("Nexus")).toBeVisible();
  await expect(page.getByText("Sign in to continue")).toBeVisible();
});

test("shows login form fields", async ({ page }) => {
  await page.goto("/login");
  await expect(page.getByLabel("Username")).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
});
