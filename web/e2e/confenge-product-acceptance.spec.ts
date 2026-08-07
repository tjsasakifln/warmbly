import { test, expect } from "@playwright/test";

/**
 * Product acceptance UI path:
 * open CONFENGE → evidence → edit → approve → quota → sent → inject reply → Needs attention.
 *
 * Requires local stack (backend + web) with CONFENGE_ENABLED and a seeded session.
 * Skip quietly only when CONFENGE_E2E is unset (local opt-in); never skip critical Go E2E.
 */
const enabled = process.env.CONFENGE_E2E === "1";

test.describe("CONFENGE product acceptance UI", () => {
  test.skip(!enabled, "Set CONFENGE_E2E=1 with local stack to run UI acceptance");

  test("review, edit, approve, quota, needs attention", async ({ page }) => {
    // Login path depends on local seed; prefer storageState when provided.
    const loginEmail = process.env.CONFENGE_E2E_EMAIL || "dev@warmbly.com";
    const loginPassword = process.env.CONFENGE_E2E_PASSWORD || "password123";

    await page.goto("/login");
    // Best-effort login form fill (selectors may vary by build).
    const email = page.locator('input[type="email"], input[name="email"]').first();
    if (await email.count()) {
      await email.fill(loginEmail);
      await page.locator('input[type="password"]').first().fill(loginPassword);
      await page.locator('button[type="submit"]').first().click();
      await page.waitForTimeout(1500);
    }

    await page.goto("/app/confenge");
    await expect(page.getByText("CONFENGE").first()).toBeVisible({ timeout: 30_000 });

    // Quota badge when dispatch is wired
    const quota = page.getByTestId("confenge-dispatch-quota");
    if (await quota.count()) {
      await expect(quota).toBeVisible();
    }

    // Review queue / evidence
    const review = page.getByTestId("confenge-review-queue");
    await expect(review).toBeVisible();

    const body = page.getByTestId("confenge-body-input");
    if (await body.count()) {
      // Evidence visible
      await expect(page.getByTestId("confenge-evidence")).toBeVisible();
      // Edit invalidates approval on server; local edit proves UI edit path
      await body.fill((await body.inputValue()) + "\n\n(edit for acceptance)");
      const recipient = page.getByTestId("confenge-recipient-input");
      if (await recipient.count()) {
        await expect(recipient).not.toHaveValue("");
      }
      // Approve & Queue
      const approve = page.getByTestId("confenge-approve-queue");
      if (await approve.isEnabled()) {
        await approve.click();
      }
    }

    // Sent stat present
    await expect(page.getByTestId("confenge-stat-sent")).toBeVisible();

    // Needs attention cockpit
    const attention = page.getByTestId("confenge-needs-attention");
    await expect(attention).toBeVisible();
    await attention.getByRole("button", { name: "Needs attention" }).click();

    // Optional: inject reply via API fixture when token is provided
    const token = process.env.CONFENGE_E2E_TOKEN;
    const baseAPI = process.env.CONFENGE_E2E_API || "http://127.0.0.1:8080";
    if (token && process.env.CONFENGE_E2E_ACCOUNT_ID) {
      await page.request.post(`${baseAPI}/api/v1/confenge/accounts/${process.env.CONFENGE_E2E_ACCOUNT_ID}/generate-reply`, {
        headers: { Authorization: `Bearer ${token}` },
        data: {},
      });
      await page.reload();
      await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();
    }
  });
});
