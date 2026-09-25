export type Instant = Date | string | number | null | undefined;
export type NumericTimeUnit = "auto" | "seconds" | "milliseconds";

const formatterCache = new Map<string, Intl.DateTimeFormat>();

function formatter(options: Intl.DateTimeFormatOptions): Intl.DateTimeFormat {
  const key = JSON.stringify(options);
  let cached = formatterCache.get(key);
  if (!cached) {
    cached = new Intl.DateTimeFormat(undefined, options);
    formatterCache.set(key, cached);
  }
  return cached;
}

export function parseInstant(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): Date | null {
  if (value == null || value === "") return null;
  if (value instanceof Date)
    return Number.isFinite(value.getTime()) ? value : null;

  let milliseconds: number;
  if (typeof value === "number") {
    if (!Number.isFinite(value)) return null;
    const seconds =
      unit === "seconds" ||
      (unit === "auto" && Math.abs(value) < 100_000_000_000);
    milliseconds = seconds ? value * 1000 : value;
  } else {
    milliseconds = Date.parse(value);
  }

  if (!Number.isFinite(milliseconds)) return null;
  const date = new Date(milliseconds);
  return Number.isFinite(date.getTime()) ? date : null;
}

export function formatLocalDate(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({ month: "short", day: "numeric" }).format(date)
    : "-";
}

export function formatLocalDateLong(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({ year: "numeric", month: "short", day: "numeric" }).format(
        date,
      )
    : "-";
}

export function formatLocalDateTime(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
      }).format(date)
    : "-";
}

export function formatLocalDateTimeWithZone(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({
        year: "numeric",
        month: "short",
        day: "numeric",
        hour: "2-digit",
        minute: "2-digit",
        timeZoneName: "short",
      }).format(date)
    : "-";
}

export function formatLocalTimeWithZone(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({
        hour: "2-digit",
        minute: "2-digit",
        timeZoneName: "short",
      }).format(date)
    : "";
}

export function formatLocalTime(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  return date
    ? formatter({ hour: "2-digit", minute: "2-digit" }).format(date)
    : "";
}

export function instantTitle(
  value: Instant,
  unit: NumericTimeUnit = "auto",
): string {
  const date = parseInstant(value, unit);
  if (!date) return "";
  return `${formatLocalDateTimeWithZone(date)} · UTC ${date.toISOString().replace("T", " ").replace(".000Z", "Z")}`;
}

export function viewerTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "Local time";
}
