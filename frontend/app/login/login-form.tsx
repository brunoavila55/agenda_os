"use client";

import { FormEvent, useState } from "react";
import { useRouter } from "next/navigation";

export default function LoginForm() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setLoading(true); setError("");
    const data = new FormData(event.currentTarget);
    const response = await fetch("/api/session/login", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: data.get("password") })
    });
    if (!response.ok) { setError("Senha inválida."); setLoading(false); return; }
    router.replace("/"); router.refresh();
  }

  return (
    <form className="login-form" onSubmit={submit}>
      <label htmlFor="password">Senha de acesso</label>
      <input id="password" name="password" type="password" autoComplete="current-password" required autoFocus />
      {error && <p className="form-error" role="alert">{error}</p>}
      <button className="button primary wide" disabled={loading}>{loading ? "Entrando…" : "Entrar no painel"}</button>
    </form>
  );
}

