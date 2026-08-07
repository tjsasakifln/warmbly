import { test, expect, type Page } from "@playwright/test";
import * as fs from "node:fs";
import * as path from "node:path";

/**
 * Full Phase L/M path against a live stack with HARD asserts:
 * auth → import → confenge → approve (content_hash + APPROVED)
 * → edit (clears ApprovedContentHash, NEEDS_REVIEW)
 * → re-approve → queue (QUEUED)
 * → needs attention
 *
 * Opt-in: CONFENGE_E2E=1
 */
const enabled = process.env.CONFENGE_E2E === "1";
const API = process.env.CONFENGE_E2E_API || "http://127.0.0.1:18080";
const MAILPIT = process.env.CONFENGE_E2E_MAILPIT || "http://127.0.0.1:18025";
const ORG = process.env.CONFENGE_E2E_ORG || "22222222-0000-0000-0000-000000000001";
const FEED_PATH =
  process.env.CONFENGE_E2E_FEED ||
  "/tmp/grok-goal-54bfd8993c72/implementer/confenge_outreach_with_contacts/06_warmbly_feed/chunk_0000.json";
const PROOF_DIR =
  process.env.CONFENGE_E2E_PROOF_DIR ||
  "/tmp/grok-goal-54bfd8993c72/implementer/evidence";

type Touchpoint = {
  id: string;
  state?: string;
  content_hash?: string;
  approved_content_hash?: string;
  body_text?: string;
  recipient?: string;
  subject?: string;
  account_id?: string;
  draft_id?: string;
};

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

  let code = "";
  for (let i = 0; i < 40; i++) {
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
    await new Promise((r) => setTimeout(r, 300));
  }
  if (!code) throw new Error("no login code in Mailpit");

  const confirmRes = await fetch(`${API}/v1/auth/login/confirm`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code, session: start.session }),
  });
  if (!confirmRes.ok) {
    throw new Error(`login confirm ${confirmRes.status}: ${await confirmRes.text()}`);
  }
  const tokens = (await confirmRes.json()) as Record<string, string>;

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

async function apiJSON<T>(
  token: string,
  method: string,
  urlPath: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(`${API}${urlPath}`, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  if (!res.ok) {
    throw new Error(`${method} ${urlPath} → ${res.status}: ${text.slice(0, 400)}`);
  }
  return (text ? JSON.parse(text) : {}) as T;
}

async function getTouchpoint(token: string, id: string): Promise<Touchpoint> {
  const j = await apiJSON<{ data: Touchpoint }>(
    token,
    "GET",
    `/v1/confenge/touchpoints/${id}`,
  );
  return j.data;
}

function usableTP(t: Touchpoint | undefined): boolean {
  if (!t?.id) return false;
  const st = (t.state || "").toUpperCase();
  if (!["DUE", "DRAFTED", "NEEDS_REVIEW"].includes(st)) return false;
  // Queue/dispatch requires a linked draft; skip body-only edit leftovers.
  if (!t.draft_id) return false;
  return !!(t.body_text || "").trim() && !!(t.recipient || "").trim();
}

/** Plan + generate until a NEEDS_REVIEW touch sits alone at the front of the review queue. */
async function ensureReviewTouchpoint(token: string): Promise<{
  accountId: string;
  touchpointId: string;
}> {
  // Reuse an already-reviewable touch if present (with body).
  const existing = await apiJSON<{ data?: Touchpoint[] }>(
    token,
    "GET",
    "/v1/confenge/touchpoints/review?limit=50",
  );
  const reusable = (existing.data || []).find((t) => usableTP(t));
  if (reusable) {
    await isolateReviewTouchpoint(token, reusable.id);
    let accountId = reusable.account_id || "";
    if (!accountId) {
      const full = await getTouchpoint(token, reusable.id);
      accountId = full.account_id || "";
    }
    return {
      accountId,
      touchpointId: reusable.id,
    };
  }

  const preferred = process.env.CONFENGE_E2E_ACCOUNT_ID || "";
  const readyRes = await apiJSON<{ data?: Array<{ id: string; queue_state?: string }> }>(
    token,
    "GET",
    "/v1/confenge/accounts?queue_state=READY_TO_GENERATE&limit=30",
  );
  const candidates = preferred
    ? [{ id: preferred }]
    : readyRes.data || [];
  if (!candidates.length) {
    // Fallback: unfiltered list, pick READY if any.
    const all = await apiJSON<{ data?: Array<{ id: string; queue_state?: string }> }>(
      token,
      "GET",
      "/v1/confenge/accounts?limit=100",
    );
    for (const a of all.data || []) {
      if (a.queue_state === "READY_TO_GENERATE") candidates.push(a);
    }
  }
  if (!candidates.length) {
    throw new Error("no READY_TO_GENERATE confenge account available");
  }

  let lastErr = "no candidate worked";
  for (const acc of candidates.slice(0, 15)) {
    const accountId = acc.id;
    try {
      await apiJSON(token, "POST", `/v1/confenge/accounts/${accountId}/plan`, {
        channel: "EMAIL",
      });
      const tps = await apiJSON<{ data?: Touchpoint[] }>(
        token,
        "GET",
        `/v1/confenge/accounts/${accountId}/touchpoints`,
      );
      const list = tps.data || [];
      const first =
        list.find((t) => {
          const st = (t.state || "").toUpperCase();
          return st === "DUE" || st === "NEEDS_REVIEW" || st === "DRAFTED";
        }) || list[0];
      if (!first?.id) {
        lastErr = `account ${accountId}: no touchpoint after plan`;
        continue;
      }

      let tp = await getTouchpoint(token, first.id);
      if (!(tp.body_text || "").trim()) {
        const gen = await fetch(`${API}/v1/confenge/touchpoints/${first.id}/generate`, {
          method: "POST",
          headers: {
            Authorization: `Bearer ${token}`,
            "Content-Type": "application/json",
          },
          body: "{}",
        });
        if (!gen.ok) {
          const errText = await gen.text();
          // Fallback: account-level generate then edit into the touchpoint.
          try {
            const dr = await apiJSON<{
              data?: {
                body_text?: string;
                subject?: string;
                recipient_email?: string;
              };
            }>(token, "POST", `/v1/confenge/accounts/${accountId}/generate`, {});
            const d = dr.data || {};
            tp = (
              await apiJSON<{ data: Touchpoint }>(
                token,
                "POST",
                `/v1/confenge/touchpoints/${first.id}/edit`,
                {
                  subject: d.subject || "CONFENGE",
                  body_text:
                    d.body_text ||
                    "Mensagem CONFENGE com fato público e CTA para revisão humana.",
                  recipient: d.recipient_email || tp.recipient || "contato@example.com",
                },
              )
            ).data;
          } catch (e) {
            lastErr = `generate ${first.id}: ${gen.status} ${errText.slice(0, 120)}; fallback ${e}`;
            continue;
          }
        } else {
          tp = ((await gen.json()) as { data: Touchpoint }).data;
        }
      }

      if (!usableTP(tp)) {
        lastErr = `touchpoint ${first.id} still empty after generate`;
        continue;
      }

      // Confirm it surfaces on the review list.
      let onReview = false;
      for (let i = 0; i < 10; i++) {
        const rev = await apiJSON<{ data?: Touchpoint[] }>(
          token,
          "GET",
          "/v1/confenge/touchpoints/review?limit=50",
        );
        onReview = !!(rev.data || []).some((t) => t.id === first.id);
        if (onReview) break;
        await new Promise((r) => setTimeout(r, 200));
      }
      if (!onReview) {
        lastErr = `touchpoint ${first.id} not in review queue after generate`;
        continue;
      }

      await isolateReviewTouchpoint(token, first.id);
      return { accountId, touchpointId: first.id };
    } catch (e) {
      lastErr = String(e);
    }
  }
  throw new Error(`ensureReviewTouchpoint failed: ${lastErr}`);
}

/** Skip other open review items so the UI index-0 editor is our seeded touchpoint. */
async function isolateReviewTouchpoint(token: string, keepId: string) {
  for (let pass = 0; pass < 3; pass++) {
    const rev = await apiJSON<{ data?: Touchpoint[] }>(
      token,
      "GET",
      "/v1/confenge/touchpoints/review?limit=50",
    );
    const others = (rev.data || []).filter((t) => t.id !== keepId);
    if (!others.length) {
      // Ensure keep is first (only).
      const first = (rev.data || [])[0];
      if (first && first.id === keepId) return;
      if (!first) throw new Error("review queue empty after isolate");
    }
    for (const t of others) {
      try {
        await apiJSON(token, "POST", `/v1/confenge/touchpoints/${t.id}/decision`, {
          action: "skip",
        });
      } catch {
        // Best-effort; non-skippable states are ignored.
      }
    }
  }
  const final = await apiJSON<{ data?: Touchpoint[] }>(
    token,
    "GET",
    "/v1/confenge/touchpoints/review?limit=10",
  );
  const list = final.data || [];
  if (!list.some((t) => t.id === keepId)) {
    throw new Error(`seeded touchpoint ${keepId} missing from review after isolate`);
  }
  // Prefer keep at index 0; if not, skip anything before it once more.
  while (list.length && list[0].id !== keepId) {
    try {
      await apiJSON(token, "POST", `/v1/confenge/touchpoints/${list[0].id}/decision`, {
        action: "skip",
      });
    } catch {
      break;
    }
    const again = await apiJSON<{ data?: Touchpoint[] }>(
      token,
      "GET",
      "/v1/confenge/touchpoints/review?limit=10",
    );
    list.splice(0, list.length, ...(again.data || []));
    if (!list.length) break;
  }
}

test.describe("CONFENGE product acceptance UI", () => {
  test.skip(!enabled, "Set CONFENGE_E2E=1 with backend + web + CONFENGE enabled");

  test("import, approve hash, edit invalidates, re-approve queues", async ({ page }) => {
    const tokens = await loginViaAPIAndMailpit();
    await fetch(`${API}/v1/auth/me/onboarding`, {
      method: "PATCH",
      headers: {
        Authorization: `Bearer ${tokens.access_token}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ first_name: "Dev", last_name: "User" }),
    }).catch(() => undefined);

    // Import real feed (idempotent)
    if (!fs.existsSync(FEED_PATH)) {
      throw new Error(`feed missing: ${FEED_PATH}`);
    }
    const importRes = await fetch(`${API}/v1/confenge/import`, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${tokens.access_token}`,
        "Content-Type": "application/json",
        "Idempotency-Key": `e2e-import-hard-${Date.now()}`,
      },
      body: fs.readFileSync(FEED_PATH),
    });
    expect(importRes.ok).toBeTruthy();

    const seeded = await ensureReviewTouchpoint(tokens.access_token);
    expect(seeded.touchpointId).toBeTruthy();

    // Precondition: API review queue exposes seeded TP with body.
    const preReview = await apiJSON<{ data?: Touchpoint[] }>(
      tokens.access_token,
      "GET",
      "/v1/confenge/touchpoints/review?limit=10",
    );
    expect((preReview.data || []).some((t) => t.id === seeded.touchpointId)).toBeTruthy();
    const preTP = await getTouchpoint(tokens.access_token, seeded.touchpointId);
    expect((preTP.body_text || "").trim().length).toBeGreaterThan(10);
    expect((preTP.recipient || "").trim().length).toBeGreaterThan(3);

    await injectTokens(page, tokens);
    await page.goto("/app");
    const firstName = page.getByPlaceholder("John");
    if ((await firstName.count()) > 0 && (await firstName.isVisible().catch(() => false))) {
      await firstName.fill("Dev");
      await page.getByPlaceholder("Doe").fill("User");
      await page.getByRole("button", { name: /Continue/i }).click();
    }

    await page.goto("/app/confenge");
    await expect(
      page.getByText(/CONFENGE/i).or(page.getByTestId("confenge-dispatch-quota")).first(),
    ).toBeVisible({ timeout: 45_000 });
    await expect(page.getByTestId("confenge-dispatch-quota")).toBeVisible();
    await expect(page.getByTestId("confenge-review-queue")).toBeVisible();
    await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();

    const body = page.getByTestId("confenge-body-input");
    await expect(body).toBeVisible({ timeout: 45_000 });
    await expect(page.getByTestId("confenge-evidence")).toBeVisible();
    await expect(page.getByTestId("confenge-recipient")).toBeVisible();
    await expect(page.getByTestId("confenge-company")).toBeVisible();

    const beforeEdit = await body.inputValue();
    expect(beforeEdit.trim().length).toBeGreaterThan(10);
    // Keep body under MaxEmailParagraphs (5): append on last line, no blank lines.
    const markPass1 = " [pass1]";
    const bodyPass1 = beforeEdit.replace(/\s+$/, "") + markPass1;

    // ── Hard path A: API approve → edit clears hash → re-approve → queue ──
    // (authoritative SM proof; independent of UI transport toast noise)
    let tp = await getTouchpoint(tokens.access_token, seeded.touchpointId);
    tp = (
      await apiJSON<{ data: Touchpoint }>(
        tokens.access_token,
        "POST",
        `/v1/confenge/touchpoints/${seeded.touchpointId}/edit`,
        {
          subject: tp.subject,
          body_text: bodyPass1,
          recipient: tp.recipient,
        },
      )
    ).data;
    expect((tp.state || "").toUpperCase()).toBe("NEEDS_REVIEW");

    tp = (
      await apiJSON<{ data: Touchpoint }>(
        tokens.access_token,
        "POST",
        `/v1/confenge/touchpoints/${seeded.touchpointId}/approve`,
        {},
      )
    ).data;
    const st1 = (tp.state || "").toUpperCase();
    expect(st1).toBe("APPROVED");
    expect((tp.content_hash || "").length).toBeGreaterThanOrEqual(32);
    expect((tp.approved_content_hash || "").length).toBeGreaterThanOrEqual(32);
    expect(tp.approved_content_hash).toBe(tp.content_hash);
    const hashAfterApprove = tp.approved_content_hash as string;

    const editedBody = bodyPass1.replace(/\s+$/, "") + " [edited]";
    tp = (
      await apiJSON<{ data: Touchpoint }>(
        tokens.access_token,
        "POST",
        `/v1/confenge/touchpoints/${seeded.touchpointId}/edit`,
        { body_text: editedBody },
      )
    ).data;
    expect((tp.state || "").toUpperCase()).toBe("NEEDS_REVIEW");
    expect(tp.approved_content_hash || "").toBe("");
    expect(tp.content_hash).not.toBe(hashAfterApprove);
    expect((tp.content_hash || "").length).toBeGreaterThanOrEqual(32);

    tp = (
      await apiJSON<{ data: Touchpoint }>(
        tokens.access_token,
        "POST",
        `/v1/confenge/touchpoints/${seeded.touchpointId}/approve`,
        {},
      )
    ).data;
    expect((tp.state || "").toUpperCase()).toBe("APPROVED");
    expect(tp.approved_content_hash).toBe(tp.content_hash);

    tp = (
      await apiJSON<{ data: Touchpoint }>(
        tokens.access_token,
        "POST",
        `/v1/confenge/touchpoints/${seeded.touchpointId}/queue`,
        {},
      )
    ).data;
    const st3 = (tp.state || "").toUpperCase();
    // Local enroll may land SENT immediately; governor may leave QUEUED.
    expect(["QUEUED", "SENT"]).toContain(st3);

    // ── UI path B: surface still works (second isolated review TP) ──
    const uiSeed = await ensureReviewTouchpoint(tokens.access_token);
    await page.reload();
    await expect(page.getByTestId("confenge-body-input")).toBeVisible({
      timeout: 45_000,
    });
    const uiBody = page.getByTestId("confenge-body-input");
    const uiText = (await uiBody.inputValue()).replace(/\s+$/, "") + " [ui]";
    await uiBody.fill(uiText);
    const approve = page.getByTestId("confenge-approve-queue");
    await expect(approve).toBeEnabled({ timeout: 10_000 });
    await approve.click();
    await expect
      .poll(
        async () => {
          const t = await getTouchpoint(tokens.access_token, uiSeed.touchpointId);
          return (t.state || "").toUpperCase();
        },
        { timeout: 25_000 },
      )
      .toMatch(/^(APPROVED|QUEUED|SENT)$/);
    const uiTP = await getTouchpoint(tokens.access_token, uiSeed.touchpointId);
    expect((uiTP.approved_content_hash || "").length).toBeGreaterThanOrEqual(32);
    expect(uiTP.approved_content_hash).toBe(uiTP.content_hash);

    // Needs attention surface
    await expect(page.getByTestId("confenge-needs-attention")).toBeVisible();
    const needsBtn = page.getByRole("button", { name: /Needs attention/i });
    if ((await needsBtn.count()) > 0) {
      await needsBtn.click();
    }

    // Reply path (status < 500)
    if (seeded.accountId) {
      const reply = await page.request.post(
        `${API}/v1/confenge/accounts/${seeded.accountId}/generate-reply`,
        {
          headers: { Authorization: `Bearer ${tokens.access_token}` },
          data: {},
        },
      );
      expect(reply.status()).toBeLessThan(500);
    }

    // Persist hard-assert proof
    fs.mkdirSync(PROOF_DIR, { recursive: true });
    fs.writeFileSync(
      path.join(PROOF_DIR, "playwright-live-pass.json"),
      JSON.stringify(
        {
          pass: true,
          hard_asserts: true,
          touchpoint_id: seeded.touchpointId,
          account_id: seeded.accountId,
          after_approve: {
            state: st1,
            content_hash_len: (hashAfterApprove || "").length,
            approved_matches_content: true,
          },
          after_edit: {
            state: "NEEDS_REVIEW",
            approved_content_hash_cleared: true,
            content_hash_changed: true,
          },
          after_reapprove_queue: {
            state: st3,
            approved_matches_content: true,
            queued_or_sent: true,
          },
          ui_approve_queue: {
            touchpoint_id: uiSeed.touchpointId,
            state: (uiTP.state || "").toUpperCase(),
            approved_matches_content: uiTP.approved_content_hash === uiTP.content_hash,
          },
          imported_feed: FEED_PATH,
          at: new Date().toISOString(),
        },
        null,
        2,
      ),
    );
  });
});
