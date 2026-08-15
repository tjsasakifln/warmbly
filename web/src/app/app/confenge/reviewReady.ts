import type { ConfengeTouchpoint } from "@/lib/api/models/app/confenge/Confenge";

// isAuthorizeReady is the dashboard "autorizáveis" / Aprovar gate.
// Missing messageability is not OK. Leftover NEEDS_REVIEW + body is not enough.
export function isAuthorizeReady(tp: ConfengeTouchpoint): boolean {
  const bodyOK = !!(tp.body_text && tp.body_text.trim());
  if (!bodyOK) {
    return false;
  }
  if (tp.consultant_sendability) {
    return tp.consultant_sendability.send_without_editing === "sim";
  }
  const recipientOK = (tp.recipient_state || "").toUpperCase() === "VALIDATED";
  const messageability = (tp.strategy_explain?.messageability || "").toUpperCase();
  const messageOK = messageability === "READY";
  const validationOK = tp.draft?.validation_ok === true;
  return recipientOK && messageOK && validationOK;
}
