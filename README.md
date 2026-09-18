# Agenda O.S. MK Solutions

Painel pessoal para consultar, organizar e planejar ordens de serviço do MK Solutions. Este primeiro marco funciona em **modo simulado explícito**: os dados são sintéticos, ficam separados por origem e nenhuma chamada ou agendamento é enviado ao ERP.

## O que já está disponível

- painel autenticado em português com resumo, filtros, lista e detalhes das O.S.;
- API Go autenticada, health checks e leitura do estado local;
- worker Go independente com sincronização automática a cada 10 minutos;
- fila persistente com reserva atômica, tentativas e recuperação de leases;
- PostgreSQL/PostGIS, migração versionada e dados iniciais da operação rural;
- sincronização manual pelo mesmo mecanismo da automática;
- estados observado, proposta e execução separados no banco;
- modo real bloqueado de forma legível enquanto faltarem contratos do MK.

O mapa, edição visual de grupos, IA e envio/reconciliação real fazem parte dos marcos seguintes. As pendências externas estão em [docs/integracao-mk.md](docs/integracao-mk.md).

O estado confirmado de cada etapa, as verificações executadas e o próximo marco recomendado estão em [docs/progresso.md](docs/progresso.md).

## Executar com Docker Compose

Requisitos: Docker com Compose v2.

```bash
cp .env.example .env
# Edite .env e troque todos os valores CHANGE_ME.
docker compose up --build
```

Abra `http://localhost:3000` e use a senha definida em `APP_PASSWORD`. O banco não publica porta no host. Para acompanhar o processamento:

```bash
docker compose logs -f api worker frontend
```

O exemplo usa `SESSION_COOKIE_SECURE=false` apenas para o acesso HTTP local. Defina `true` na implantação com HTTPS.

Uma instalação que já tenha aplicado uma migração não deve alterar esse arquivo. Migrações futuras devem receber novo número e ser aplicadas por um mecanismo de deploy; a montagem em `docker-entrypoint-initdb.d` serve ao primeiro bootstrap.

## API

Rotas públicas: `GET /health/live` e `GET /health/ready`. As rotas em `/api/v1` exigem `Authorization: Bearer $APP_API_TOKEN`.

Principais rotas:

- `GET /api/v1/dashboard`
- `GET /api/v1/orders?status=open&operation_id=<uuid>`
- `GET /api/v1/operations`
- `GET /api/v1/service-types`
- `PUT /api/v1/operations/{id}/service-types`
- `POST /api/v1/sync-runs`
- `GET /api/v1/sync-runs/latest`

O frontend atua como BFF: o token interno nunca é enviado ao navegador.

## Desenvolvimento

Backend usa Go 1.26 e frontend usa Next.js 16.3.3. O host atual pode não ter Go instalado; os builds oficiais rodam nos contêineres. Com as ferramentas locais instaladas:

```bash
cd backend && gofmt -w . && go test ./...
cd frontend && npm ci && npm run lint && npm run build
```

## Segurança operacional

- não use credenciais reais em fixtures, commits ou variáveis `NEXT_PUBLIC_*`;
- mantenha `APP_MODE=simulation` até os contratos do ambiente estarem confirmados;
- publique o painel somente atrás de HTTPS;
- o modo simulado nunca faz fallback silencioso nem chama o MK.
