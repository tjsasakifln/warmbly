export type OperatorNotifyResult = "notified" | "fallback" | "noop";

export function operatorAlertTitle(): string {
  return "Novo lead real no INBOUND NOW";
}

export function operatorAlertBody(unacknowledgedReal: number): string {
  const n = Math.max(0, Math.floor(unacknowledgedReal));
  if (n === 1) return "1 lead real sem reconhecimento";
  return `${n} leads reais sem reconhecimento`;
}

export function shouldNotifyOperatorAlert(unacknowledgedReal: number, lastNotifiedCount: number): boolean {
  return unacknowledgedReal > 0 && unacknowledgedReal !== lastNotifiedCount;
}

export function notifyOperatorAlert(opts: {
  permission: NotificationPermission | "unsupported";
  unacknowledgedReal: number;
  notify?: (title: string, body: string) => void;
  fallback?: () => void;
}): OperatorNotifyResult {
  if (opts.unacknowledgedReal <= 0) return "noop";
  const title = operatorAlertTitle();
  const body = operatorAlertBody(opts.unacknowledgedReal);
  if (opts.permission === "granted" && opts.notify) {
    opts.notify(title, body);
    return "notified";
  }
  opts.fallback?.();
  return "fallback";
}

export function formatReceivedAgoSaoPaulo(iso?: string, now = Date.now()): string {
  if (!iso) return "UNKNOWN";
  const ts = Date.parse(iso);
  if (Number.isNaN(ts)) return "UNKNOWN";
  const delta = Math.max(0, now - ts);
  const sec = Math.floor(delta / 1000);
  if (sec < 60) return `há ${sec}s`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `há ${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `há ${hr}h`;
  return `há ${Math.floor(hr / 24)}d`;
}

export function formatSaoPauloClock(iso?: string): string {
  if (!iso) return "UNKNOWN";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "UNKNOWN";
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: "America/Sao_Paulo",
    day: "2-digit",
    month: "2-digit",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  }).format(d);
}
