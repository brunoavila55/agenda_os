import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { SESSION_COOKIE, validSessionToken } from "@/lib/session";
import Dashboard from "./dashboard";

export default async function Home() {
  const jar = await cookies();
  if (!validSessionToken(jar.get(SESSION_COOKIE)?.value)) redirect("/login");
  return <Dashboard />;
}
