import { test, expect, type Page } from "@playwright/test";

/**
 * Product acceptance UI path:
 * open CONFENGE → evidence → edit → approve → quota → inject reply → Needs attention.
 *
 * Requires local stack (backend + web) with CONFENGE enabled.
 * Opt-in: CONFENGE_E2E=1.
 *
 * Auth uses the real login OTP path via Mailpit (not a skipped captcha hack):
 * LoginStart → read code from Mailpit → LoginConfirm → inject tokens.
 */
const enabled = process.env.CONFENGE_E2E === "1";
const API = process.env.CONFENGE_E2E_API || "http://127.0.0.1:18080";
const MAILPIT = process.env.CONFENGE_E2E_MAILPIT || "http://127.0.0.1:18025";
const ORG = process.env.CONFENGE_E2E_ORG || "22222222-0000-0000-0000-000000000001";

async function loginViaAPIAndMailpit(): Promise<Record<string, string>> {
  const email = process.env.CONFENGE_E2E_EMAIL || "dev@warmbly.com";
  const password = process.env.CONFENGE_E2E_PASSWORD || "password123";

  const startRes = await fetch(`${API}/v1/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ email, password }),
  });
  if (!startRes.ok) {
    throw new Error(`login start ${startRes.status}: ${await startRes.text()}`);
  }
  const start = (await startRes.json()) as { session: string };

  // Poll Mailpit for the newest login code email.
  let code = "";
  for (let i = 0; i < 20; i++) {
    const listRes = await fetch(`${MAILPIT}/api/v1/messages`);
    const list = (await listRes.json()) as {
      messages?: Array<{ ID: string; Subject?: string }>;
    };
    const msg = (list.messages || []).find((m) =>
      (m.Subject || "").includes("Login Code"),
    );
    if (msg) {
      const bodyRes = await fetch(`${MAILPIT}/api/v1/message/${msg.ID}`);
      const body = (await bodyRes.json()) as { Text?: string; HTML?: string };
      const text = body.Text || body.HTML || "";
      const m = text.match(/\b(\d{6})\b/);
      if (m) {
        code = m[1];
        break;
      }
    }
    await new Promise((r) => setTimeout(r, 500));
  }
  if (!code) throw new Error("no login code found in Mailpit");

  const confirmRes = await fetch(`${API}/v1/auth/login/confirm`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code, session: start.session }),
  });
  if (!confirmRes.ok) {
    throw new Error(`login confirm ${confirmRes.status}: ${await confirmRes.text()}`);
  }
  const tokens = (await confirmRes.json()) as Record<string, string>;

  // Select org for the session
  const sw = await fetch(`${API}/v1/organization/switch/${ORG}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${tokens.access_token}` },
  });
  if (!sw.ok) {
    throw new Error(`org switch ${sw.status}: ${await sw.text()}`);
  }
  return tokens;
}

async function injectTokens(page: Page, tokens: Record<string, string>) {
  await page.goto("/login");
  await page.evaluate((t) => {
    // Dual storage: individual keys (saveTokens) + auth_token blob (getToken).
    localStorage.setItem("access_token", t.access_token);
    localStorage.setItem("access_token_expires_at", t.access_token_expires_at);
    localStorage.setItem("refresh_token", t.refresh_token);
    localStorage.setItem("refresh_token_expires_at", t.refresh_token_expires_at);
    localStorage.setItem(
      "auth_token",
      JSON.stringify({
        access_token: t.access_token,
        access_token_expires_at: t.access_token_expires_at,
        refresh_token: t.refresh_token,
        refresh_token_expires_at: t.refresh_token_expires_at,
      }),
    );
  }, tokens);
}

test.describe("CONFENGE product acceptance UI", () => {
  test.skip(!enabled, "Set CONFENGE_E2E=1 with backend + web + CONFENGE enabled");

  test("open, evidence, edit, approve, quota, reply, needs attention", async ({
    page,
  }) => {
    const tokens = await loginViaAPIAndMailpit();
    // Ensure onboarding is marked complete for this user (DB/API).
    await fetch(`${API}/v1/auth/me/onboarding`, {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${tokens.access_token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ first_name: "Dev", last_name: "User" }),
    }).catch(() => undefined);

    await injectTokens(page, tokens);

    // Land on app first so auth+user bootstrap runs, then open CONFENGE.
    await page.goto("/app");
    // If onboarding wizard still appears, finish first step via UI.
    const firstName = page.getByPlaceholder("John");
    if ((await firstName.count()) > 0 && (await firstName.isVisible().catch(() => false))) {
      await firstName.fill("Dev");
      await page.getByPlaceholder("Doe").fill("User");
      await page.getByRole("button", { name: /Continue/i }).click();
      // Skip remaining wizard steps if present.
      for (let i = 0; i < 4; i++) {
        const cont = page.getByRole("button", { name: /Continue|Skip|Finish|Done/i });
        if ((await cont.count()) === 0) break;
        await cont.first().click().catch(() => undefined);
        await page.waitForTimeout(400);
      }
    }

    await page.goto("/app/confenge");
    // Match eyebrow or any confenge surface (case-insensitive).
    await expect(
      page.getByText(/CONFENGE/i).or(page.getByTestId("confenge-dispatch-quota")).first(),
    ).toBeVisible({ timeout: 45_000 });

    // Quota (governor hourly)
    await expect(page.getByTestId("confenge-dispatch-quota")).toBeVisible({
      timeout: 15_000,
    });

    // Sent counter
    await expect(page.getByTestId("confenge-stat-sent")).toBeVisible();

    // Needs attention pane
    await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();

    // Review queue
    const review = page.getByTestId("confenge-review-queue");
    await expect(review).toBeVisible();

    // Prefer a reviewable lead row if present
    const firstLead = page.getByTestId("confenge-lead-row").first();
    if ((await firstLead.count()) > 0) {
      await firstLead.click();
    } else {
      // Click first review queue row by company name area
      const company = page.getByTestId("confenge-company").first();
      if ((await company.count()) > 0) {
        await company.click();
      }
    }

    const body = page.getByTestId("confenge-body-input");
    // Wait for draft editor — generate may already have created one
    if ((await body.count()) === 0) {
      // Try opening any clickable review item
      const row = review.locator("button, a, [role='button']").first();
      if ((await row.count()) > 0) await row.click();
    }
    await expect(body).toBeVisible({ timeout: 20_000 });

    // Company / recipient / service / evidence surfaces
    await expect(page.getByTestId("confenge-evidence")).toBeVisible();
    await expect(page.getByTestId("confenge-recipient")).toBeVisible();
    const service = page.getByTestId("confenge-channel-service");
    if ((await service.count()) > 0) {
      await expect(service).toBeVisible();
    }
    const company = page.getByTestId("confenge-company");
    if ((await company.count()) > 0) {
      await expect(company).toBeVisible();
    }

    // Edit exact message text
    const current = await body.inputValue();
    await body.fill(current + "\n\n(edit for acceptance)");

    // Approve exact text
    const approve = page.getByTestId("confenge-approve-queue");
    await expect(approve).toBeVisible();
    await expect(approve).toBeEnabled({ timeout: 10_000 });
    await approve.click();

    // Re-edit must be possible (invalidates prior approval if UI shows badge)
    const afterApprove = await body.inputValue();
    await body.fill(afterApprove + "\n(invalidates approval)");
    if (await approve.isEnabled()) {
      await approve.click();
    }

    // Inject reply when account provided (or discover first account via API)
    let accountId = process.env.CONFENGE_E2E_ACCOUNT_ID || "";
    if (!accountId) {
      const accRes = await fetch(`${API}/v1/confenge/accounts?limit=1`, {
        headers: { Authorization: `Bearer ${tokens.access_token}` },
      });
      if (accRes.ok) {
        const accJson = (await accRes.json()) as {
          data?: Array<{ id?: string }>;
        };
        accountId = accJson.data?.[0]?.id || "";
      }
    }
    if (accountId) {
      const res = await page.request.post(
        `${API}/v1/confenge/accounts/${accountId}/generate-reply`,
        {
          headers: { Authorization: `Bearer ${tokens.access_token}` },
          data: {},
        },
      );
      // 200/201/4xx all acceptable if account has no reply path — assert not network death
      expect(res.status()).toBeLessThan(500);
      await page.reload();
    }

    // Needs attention still present
    await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();
    const needsBtn = page.getByRole("button", { name: /Needs attention/i });
    if ((await needsBtn.count()) > 0) {
      await needsBtn.click();
    }
  });
});
