import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { SESSION_COOKIE, validSessionToken } from "@/lib/session";
import LoginForm from "./login-form";

export default async function LoginPage() {
  const jar = await cookies();
  if (validSessionToken(jar.get(SESSION_COOKIE)?.value)) redirect("/");
  return (
    <main className="login-page">
      <section className="login-card">
        <div className="brand-mark" aria-hidden="true">OS</div>
        <p className="eyebrow">MK Solutions · planejamento local</p>
        <h1>Organize o dia antes de colocar a equipe na estrada.</h1>
        <p className="muted">Acesse o painel pessoal para revisar ordens, localizações e propostas.</p>
        <LoginForm />
        <p className="login-note">Ambiente sem cadastro público. O acesso é definido pelo administrador.</p>
      </section>
      <aside className="login-visual" aria-hidden="true">
        <div className="route-line route-one" />
        <div className="route-line route-two" />
        <span className="map-pin pin-one" />
        <span className="map-pin pin-two" />
        <span className="map-pin pin-three" />
        <div className="visual-copy"><strong>Planejamento rural</strong><span>proximidade com revisão humana</span></div>
      </aside>
    </main>
  );
}

