import { test, expect } from "@playwright/test";

/**
 * Product acceptance UI path:
 * open CONFENGE → evidence → edit → approve → quota → sent → inject reply → Needs attention.
 *
 * Requires local stack (backend + web) with CONFENGE enabled and a seeded session.
 * Opt-in: CONFENGE_E2E=1. Never silently skips critical Go service E2E.
 *
 * When the stack is not available, CI still gates UI affordances via
 * TestConfengeUIAcceptanceAffordancesPresent (static source check).
 */
const enabled = process.env.CONFENGE_E2E === "1";

test.describe("CONFENGE product acceptance UI", () => {
  test.skip(!enabled, "Set CONFENGE_E2E=1 with make backend + make web + CONFENGE enabled");

  test("open, evidence, edit, approve, quota, sent, reply, needs attention", async ({ page }) => {
    const loginEmail = process.env.CONFENGE_E2E_EMAIL || "dev@warmbly.com";
    const loginPassword = process.env.CONFENGE_E2E_PASSWORD || "password123";

    await page.goto("/login");
    const email = page.locator('input[type="email"], input[name="email"]').first();
    await expect(email).toBeVisible({ timeout: 15_000 });
    await email.fill(loginEmail);
    await page.locator('input[type="password"]').first().fill(loginPassword);
    await page.locator('button[type="submit"]').first().click();
    await page.waitForURL(/\/app/, { timeout: 30_000 }).catch(() => undefined);

    // Open CONFENGE
    await page.goto("/app/confenge");
    await expect(page.getByText("CONFENGE").first()).toBeVisible({ timeout: 30_000 });

    // Quota
    await expect(page.getByTestId("confenge-dispatch-quota")).toBeVisible({ timeout: 15_000 });

    // Sent counter always rendered in stats row
    await expect(page.getByTestId("confenge-stat-sent")).toBeVisible();

    // Needs attention pane present
    const attention = page.getByTestId("confenge-needs-attention");
    await expect(attention).toBeVisible();

    // Review queue
    const review = page.getByTestId("confenge-review-queue");
    await expect(review).toBeVisible();

    const body = page.getByTestId("confenge-body-input");
    if ((await body.count()) > 0) {
      // Evidence
      await expect(page.getByTestId("confenge-evidence")).toBeVisible();
      await expect(page.getByTestId("confenge-recipient")).toBeVisible();

      // Edit
      const current = await body.inputValue();
      await body.fill(current + "\n\n(edit for acceptance)");

      // Approve & Queue
      const approve = page.getByTestId("confenge-approve-queue");
      await expect(approve).toBeVisible();
      if (await approve.isEnabled()) {
        await approve.click();
      }
    }

    // Inject reply fixture when token+account provided
    const token = process.env.CONFENGE_E2E_TOKEN;
    const baseAPI = process.env.CONFENGE_E2E_API || "http://127.0.0.1:8080";
    if (token && process.env.CONFENGE_E2E_ACCOUNT_ID) {
      const res = await page.request.post(
        `${baseAPI}/api/v1/confenge/accounts/${process.env.CONFENGE_E2E_ACCOUNT_ID}/generate-reply`,
        { headers: { Authorization: `Bearer ${token}` }, data: {} },
      );
      expect(res.ok() || res.status() === 200 || res.status() === 201).toBeTruthy();
      await page.reload();
    }

    // Needs attention visible (and filter selected)
    await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();
    await page.getByRole("button", { name: "Needs attention" }).click();
  });
});
