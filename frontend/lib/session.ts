import { createHmac, timingSafeEqual } from "node:crypto";

export const SESSION_COOKIE = "agenda_os_session";
const SESSION_VALUE = "authenticated:v1";

function secret(): string {
  const value = process.env.SESSION_SECRET;
  if (!value || value.length < 32) throw new Error("SESSION_SECRET deve ter pelo menos 32 caracteres");
  return value;
}

export function createSessionToken(): string {
  return createHmac("sha256", secret()).update(SESSION_VALUE).digest("base64url");
}

export function validSessionToken(value: string | undefined): boolean {
  if (!value) return false;
  const expected = Buffer.from(createSessionToken());
  const received = Buffer.from(value);
  return received.length === expected.length && timingSafeEqual(received, expected);
}

