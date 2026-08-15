import { describe, expect, it } from "vitest";
import { isAuthorizeReady } from "./reviewReady";
import type { ConfengeTouchpoint } from "@/lib/api/models/app/confenge/Confenge";

function tp(partial: Partial<ConfengeTouchpoint>): ConfengeTouchpoint {
  return {
    id: "t1",
    organization_id: "o",
    account_id: "a",
    ordinal: 1,
    channel: "EMAIL",
    purpose: "SIGNAL",
    due_at: "",
    state: "NEEDS_REVIEW",
    recipient: "ana@exemplo.com.br",
    subject: "Aditivo publicado",
    body_text: "Olá Ana,\n\nSou da CONFENGE.\n\nPosso te mostrar o que eu checaria?",
    content_hash: "",
    approved_content_hash: "",
    service_code: "ADITIVOS",
    fact_used: "aditivo 2",
    ...partial,
  };
}

describe("isAuthorizeReady", () => {
  it("accepts send_without_editing=sim with a body", () => {
    expect(
      isAuthorizeReady(
        tp({
          consultant_sendability: { send_without_editing: "sim" },
        }),
      ),
    ).toBe(true);
  });

  it("rejects leftover NEEDS_REVIEW body when pack says nao and validation is missing", () => {
    expect(
      isAuthorizeReady(
        tp({
          recipient_state: "VALIDATED",
          consultant_sendability: { send_without_editing: "nao" },
          draft: { validation_ok: false } as ConfengeTouchpoint["draft"],
        }),
      ),
    ).toBe(false);
  });

  it("rejects missing messageability even with VALIDATED + body", () => {
    expect(
      isAuthorizeReady(
        tp({
          recipient_state: "VALIDATED",
          strategy_explain: {},
          draft: { validation_ok: true } as ConfengeTouchpoint["draft"],
        }),
      ),
    ).toBe(false);
  });

  it("rejects empty body even when pack says sim", () => {
    expect(
      isAuthorizeReady(
        tp({
          body_text: "   ",
          consultant_sendability: { send_without_editing: "sim" },
        }),
      ),
    ).toBe(false);
  });

  it("accepts VALIDATED + READY + validation_ok + body without a pack", () => {
    expect(
      isAuthorizeReady(
        tp({
          recipient_state: "VALIDATED",
          strategy_explain: { messageability: "READY" },
          draft: { validation_ok: true } as ConfengeTouchpoint["draft"],
        }),
      ),
    ).toBe(true);
  });
});
