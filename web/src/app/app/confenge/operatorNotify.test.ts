import { describe, expect, it, vi } from "vitest";
import {
  formatReceivedAgoSaoPaulo,
  formatSaoPauloClock,
  notifyOperatorAlert,
  operatorAlertBody,
  operatorAlertTitle,
  shouldNotifyOperatorAlert,
} from "./operatorNotify";

describe("operator inbound notify", () => {
  it("uses a generic title without lead PII", () => {
    expect(operatorAlertTitle()).toBe("Novo lead real no INBOUND NOW");
    expect(operatorAlertBody(1)).not.toMatch(/@|cnpj|ana/i);
  });

  it("falls back when permission is denied", () => {
    const fallback = vi.fn();
    const notify = vi.fn();
    const result = notifyOperatorAlert({
      permission: "denied",
      unacknowledgedReal: 2,
      notify,
      fallback,
    });
    expect(result).toBe("fallback");
    expect(notify).not.toHaveBeenCalled();
    expect(fallback).toHaveBeenCalledTimes(1);
  });

  it("notifies only when permission is granted", () => {
    const fallback = vi.fn();
    const notify = vi.fn();
    const result = notifyOperatorAlert({
      permission: "granted",
      unacknowledgedReal: 1,
      notify,
      fallback,
    });
    expect(result).toBe("notified");
    expect(notify).toHaveBeenCalledWith("Novo lead real no INBOUND NOW", "1 lead real sem reconhecimento");
    expect(fallback).not.toHaveBeenCalled();
  });

  it("does not notify when the real queue is empty", () => {
    expect(notifyOperatorAlert({ permission: "granted", unacknowledgedReal: 0 })).toBe("noop");
    expect(shouldNotifyOperatorAlert(0, 0)).toBe(false);
  });

  it("formats America/Sao_Paulo without rewriting the stored UTC instant", () => {
    const iso = "2026-08-19T15:00:00Z";
    expect(formatSaoPauloClock(iso)).toContain("12:00");
    expect(iso).toBe("2026-08-19T15:00:00Z");
    expect(formatReceivedAgoSaoPaulo(iso, Date.parse("2026-08-19T15:05:00Z"))).toBe("há 5m");
  });
});
