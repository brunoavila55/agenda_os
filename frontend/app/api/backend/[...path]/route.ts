import { cookies, headers } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { SESSION_COOKIE, validSessionToken } from "@/lib/session";

async function proxy(request: NextRequest, context: { params: Promise<{ path: string[] }> }) {
  const jar = await cookies();
  if (!validSessionToken(jar.get(SESSION_COOKIE)?.value)) return NextResponse.json({ error: { message: "Sessão expirada." } }, { status: 401 });

  if (!["GET", "HEAD"].includes(request.method)) {
    const requestHeaders = await headers();
    const origin = requestHeaders.get("origin");
    if (origin && origin !== request.nextUrl.origin) return NextResponse.json({ error: { message: "Origem inválida." } }, { status: 403 });
  }

  const { path } = await context.params;
  const base = process.env.API_INTERNAL_URL;
  const token = process.env.APP_API_TOKEN;
  if (!base || !token) return NextResponse.json({ error: { message: "Backend não configurado." } }, { status: 503 });
  const target = new URL(`/api/v1/${path.join("/")}`, base);
  target.search = request.nextUrl.search;
  const body = ["GET", "HEAD"].includes(request.method) ? undefined : await request.arrayBuffer();
  const upstream = await fetch(target, {
    method: request.method, body, cache: "no-store",
    headers: { "Authorization": `Bearer ${token}`, "Content-Type": request.headers.get("content-type") ?? "application/json" }
  }).catch(() => null);
  if (!upstream) return NextResponse.json({ error: { message: "API local indisponível." } }, { status: 503 });
  return new NextResponse(upstream.body, { status: upstream.status, headers: { "Content-Type": upstream.headers.get("content-type") ?? "application/json", "Cache-Control": "no-store" } });
}

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;

