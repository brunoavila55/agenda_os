import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Agenda O.S.",
  description: "Planejamento diário de ordens de serviço"
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="pt-BR">
      <body>{children}</body>
    </html>
  );
}
