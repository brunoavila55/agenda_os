import { timingSafeEqual } from "node:crypto";
import { NextRequest, NextResponse } from "next/server";
import { createSessionToken, SESSION_COOKIE } from "@/lib/session";

export async function POST(request: NextRequest) {
  const contentType = request.headers.get("content-type") ?? "";
  if (!contentType.startsWith("application/json")) return NextResponse.json({ error: "Formato inválido." }, { status: 415 });
  const body = (await request.json().catch(() => null)) as { password?: unknown } | null;
  const configured = process.env.APP_PASSWORD ?? "";
  const supplied = typeof body?.password === "string" ? body.password : "";
  const left = Buffer.from(configured); const right = Buffer.from(supplied);
  const valid = configured.length >= 12 && left.length === right.length && timingSafeEqual(left, right);
  if (!valid) return NextResponse.json({ error: "Credenciais inválidas." }, { status: 401 });
  const response = NextResponse.json({ authenticated: true });
  response.cookies.set(SESSION_COOKIE, createSessionToken(), {
    httpOnly: true, sameSite: "strict", secure: process.env.SESSION_COOKIE_SECURE !== "false", path: "/", maxAge: 60 * 60 * 12
  });
  return response;
}
