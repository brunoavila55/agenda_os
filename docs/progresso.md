# Progresso do desenvolvimento

Atualizado em 18 de setembro de 2026.

Este documento registra o que foi confirmado no código atual. Ele não representa validação contra o ERP MK real.

## Resumo

O projeto possui o primeiro marco funcional em modo simulado: frontend autenticado, API Go, worker independente, PostgreSQL/PostGIS, fila persistente e painel de consulta. O modo real continua bloqueado deliberadamente enquanto os contratos pendentes do MK não forem confirmados.

| Etapa | Estado | Evidência atual |
| --- | --- | --- |
| 1. Estrutura, contratos e modo simulado | Concluída para o escopo inicial | API, worker, frontend, Compose, fixtures sintéticas e documentação de bloqueios |
| 2. Banco e tarefas | Parcial avançada | Esquema, migrações de deploy com checksum, jobs com lease e propostas persistentes; concorrência de tarefas e sincronização repetida agora têm testes automatizados contra PostgreSQL/PostGIS real |
| 3. Cliente MK e sincronização real | Não iniciada | Não existe adaptador MK; `APP_MODE=real` bloqueia o worker explicitamente |
| 4. Painel operacional | Parcial avançada | Resumo, lista paginada, busca, detalhes, tipos, sincronização e planejamento diário editável |
| 5. Localização e grupos calculados | Parcial avançada | Agrupamento em metros, grupos fixados, recálculo, correção manual e visualização por coordenadas; provedor de mapa viário ainda não definido |
| 6. IA | Conectada ao planejamento | Criação e recálculo de proposta chamam a fronteira de Workers AI com cliente substituível; grupos fixados e propostas aprovadas nunca são tocados; qualquer falha preserva o agrupamento determinístico já calculado; nenhum modelo Cloudflare real foi testado ainda |
| 7. Agendamento | Parcial inicial | Prévia revalida itens e expõe bloqueios; não cria comandos nem chama o MK |
| 8. Piloto rural | Pendente | Depende da integração real e da validação operacional |
| 9. Implantação e fibra | Pendente | Depende das etapas anteriores, infraestrutura definida e testes de carga/restauração |

## Funcionalidades confirmadas

- execução separada da API e do worker;
- autenticação do painel por senha e sessão HTTP-only;
- BFF do Next.js, sem exposição do token interno ao navegador;
- health checks de processo e prontidão local;
- operação rural e catálogo sintético persistidos por migração;
- sincronização automática e manual pelo mesmo mecanismo de fila;
- reserva de tarefas com `FOR UPDATE SKIP LOCKED`, lease e recuperação de tarefas expiradas;
- atualização idempotente das ordens simuladas;
- armazenamento separado para estado observado, propostas e comandos de agendamento;
- listagem e filtro de ordens por situação e operação;
- detalhes da ordem, origem da localização e versão observada;
- seleção persistente dos tipos elegíveis por ID;
- aviso permanente de modo simulado e ausência de fallback silencioso para fixtures;
- consultas operacionais isoladas pela origem ativa (`simulation` ou `real`);
- proposta diária criada de forma idempotente por operação/data;
- proximidade calculada no PostGIS com `ST_DWithin` sobre `geography`;
- grupos e pendências editáveis, com equipe e responsável de agenda separados;
- versão otimista na edição e revalidação das ordens antes da aprovação local;
- serviço de migração de deploy com trava, transação e checksum;
- intervalo automático respeitado por operação;
- falha de sincronização registrada fora da transação que atualiza ordens;
- grupos podem ser fixados e preservados durante recálculo;
- novas ordens são incorporadas sem alterar grupos fixados;
- localização manual tem versão otimista e conflito `409`;
- mudança posterior do endereço invalida a confiança sem apagar a correção;
- mapa local posiciona marcadores pelas coordenadas armazenadas, sem inventar malha viária;
- prévia de agendamento revalida cada ordem e mantém envio bloqueado;
- API de ordens paginada, com busca executada no PostgreSQL;
- cliente Workers AI configurável com JSON Schema e timeout controlado pelo backend;
- validação de IA rejeita IDs inventados, duplicados, omitidos, ordens sem posição e grupos desconectados;
- metadados de sugestões possuem migração própria, sem persistir prompts completos.
- reserva de tarefas (`FOR UPDATE SKIP LOCKED`), expiração e recuperação de lease, esgotamento de tentativas, atraso de nova tentativa e o índice único de sincronização por operação têm testes automatizados contra PostgreSQL/PostGIS real, incluindo reserva concorrente por múltiplos workers;
- a sincronização simulada tem teste automatizado de execução repetida (idempotência sem duplicar ordens nem incrementar versão sem mudança real) e de falha parcial (nenhuma ordem persistida e execução registrada como `failed`) contra PostgreSQL/PostGIS real;
- criar e recalcular uma proposta chamam `Store.SuggestGrouping`, que pede uma sugestão ao cliente Workers AI configurado (interface substituível, `nil` por padrão) sobre a parte ainda ajustável da proposta (pendentes e grupos não fixados com posição), valida a resposta de novo no backend independentemente do que o cliente já validou, e só então aplica; grupos fixados e propostas aprovadas nunca entram na chamada;
- a chamada ao Workers AI nunca ocorre com uma transação de banco aberta: o cálculo determinístico é sempre gravado primeiro, a sugestão é buscada depois, e sua aplicação usa controle de versão otimista — uma edição concorrente durante a chamada faz a sugestão ser descartada em vez de sobrescrever o estado mais novo;
- falha de rede, resposta inválida ou sugestão descartada por conflito preservam o agrupamento determinístico já calculado e devolvem a proposta normalmente; o painel mostra a origem do agrupamento (`generator`: cálculo local, recálculo local ou sugestão da IA);
- toda sugestão (validada ou rejeitada) é registrada em `grouping_suggestions` com modelo, versão do prompt, assinatura da entrada e resultado ou erro sanitizado.

## Lacunas técnicas prioritárias

### Fundação e confiabilidade

- evoluir o token interno único para papéis distintos caso novos usuários ou perfis sejam adicionados;
- os novos testes em `backend/internal/store` e `backend/internal/sync` cobrem o worker de sincronização simulada e a fila de tarefas; ainda não há teste automatizado equivalente para o próprio laço do processo `worker` (parada, retomada do zero como processo separado).

### Planejamento no simulador

- permitir superseder ou reabrir explicitamente uma proposta já aprovada;
- definir a convenção diária de horários antes de criar solicitações de agendamento.

### Localização e agrupamento

- escolher provedor de mapa/geocodificação e avaliar cobertura rural, termos e custo;
- adicionar geocodificação verificável e manter cache conforme as regras do provedor.

### Integração MK

- criar o adaptador MK e DTOs separados dos modelos internos;
- implementar autenticação e cache seguro do token retornado;
- atualizar manualmente o catálogo de tipos;
- importar ordens elegíveis e continuar reconciliando ordens acompanhadas;
- diferenciar erro HTTP, funcional, autenticação, permissão e resposta malformada;
- implementar limites de concorrência, timeout e repetição apenas para leituras seguras.

### IA e agendamento

- nenhum modelo Cloudflare real foi escolhido nem testado; a fronteira está conectada mas segue sem uso em produção até isso ser decidido;
- avaliar se o painel deve permitir pedir uma nova sugestão sob demanda (hoje ela só é buscada automaticamente ao criar ou recalcular);
- persistir a aprovação e o comando antes da chamada externa;
- revalidar cada ordem imediatamente antes do envio;
- confirmar o resultado por leitura e tratar resultados incertos sem reenvio cego;
- preservar resultados individuais em lotes parcialmente concluídos.

## Bloqueios externos

O desenvolvimento local pode continuar no simulador, mas a integração real depende de:

1. código dos tipos rurais escolhidos;
2. endpoint e resposta anonimizados da listagem por tipo/situação;
3. base, porta e habilitação da API Node;
4. retorno completo de uma O.S. e códigos de situação/agendamento;
5. disponibilidade de vínculo inequívoco com conexão e coordenadas;
6. diagnóstico dos serviços 38/39 pelo suporte MK;
7. cadastros reais de equipe, técnicos e agenda responsável;
8. convenção de início/fim do planejamento diário e confirmação do agendamento;
9. release, permissões, restrição de IP e limites de consumo;
10. servidor, domínio, fuso, provedor geográfico e modelo Workers AI definitivos.

Detalhes e evidências conhecidas permanecem em [integração MK](integracao-mk.md).

## Verificações desta revisão

Executadas em 18 de setembro de 2026:

- `npm run lint`: aprovado;
- `npm run build`: aprovado com Next.js 16.3.3;
- compilação dos quatro binários Go na imagem Docker (`api`, `worker`, `healthcheck` e `migrate`): aprovada;
- `go test ./...` dentro da imagem de build: aprovado;
- pacotes `cmd/migrate`, `internal/config`, `internal/httpapi`, `internal/llm` e `internal/store`: testes unitários aprovados;
- `internal/sync` (sincronização simulada) agora tem testes próprios contra PostgreSQL/PostGIS real, descritos abaixo; o laço do processo `worker` em si continua sem teste automatizado dedicado;
- `scripts/integration-test.sh`: aprovado em ambiente Compose descartável;
- teste integrado com PostgreSQL/PostGIS: migrações `001`–`006`, sincronização simulada, grupos fixados, nova ordem, recálculo, correção manual, invalidação por endereço, conflito `409`, aprovação, prévia bloqueada, busca e paginação;
- todas as rotas da API verificadas contra acesso sem token e parser JSON testado contra campos/documentos extras;
- adoção e segunda execução idempotente do executor de migrações: aprovadas;
- falha forçada de sincronização: registrada como `failed` após rollback da atualização de ordens;
- novos testes de `backend/internal/store` e `backend/internal/sync` contra PostgreSQL/PostGIS real, com `go vet` e `go test ./... -race -count=1` repetido cinco vezes seguidas sem falha: reserva concorrente de tarefas por múltiplos workers nunca duplica uma reserva, lease expirada é recuperada com tentativa incrementada, tarefa esgotada para de ser reservada, atraso de nova tentativa é respeitado, `EnqueueSync`/`EnqueueDueSync` concorrentes criam exatamente uma tarefa ativa por operação, e a sincronização simulada é idempotente em execução repetida e preserva o estado anterior em falha parcial;
- essa rodada usou Podman (imagem `golang:1.26-alpine` e `postgis/postgis:17-3.5-alpine`) neste ambiente por não haver acesso ao daemon Docker na sessão; o script versionado `scripts/postgres-tests.sh` (Docker Compose, como os demais scripts do projeto) reproduz o mesmo fluxo mas ainda não foi executado literalmente — confirmar com Docker antes de considerar o script em si validado;
- um bug real de concorrência foi encontrado e corrigido durante essa verificação: `go test ./...` roda os pacotes em processos paralelos, e dois processos executando `CREATE EXTENSION IF NOT EXISTS postgis` ao mesmo tempo contra o mesmo banco de teste colidiam (`pg_extension_name_index`); a aplicação de migração de teste agora serializa com `pg_advisory_lock`, como o executor de migrações de produção já fazia;
- um teste inicial de expiração de lease baseado em `time.Sleep` real se mostrou instável sob `-race` (a lease podia vencer antes da verificação seguinte); os testes de lease e de atraso de nova tentativa agora forçam o vencimento via SQL em vez de dependerem de tempo real decorrido;
- `Store.SuggestGrouping` tem cinco testes contra PostgreSQL/PostGIS real com um cliente Workers AI substituído por um dublê: aplica uma sugestão validada e registra `grouping_suggestions` como `validated`; descarta uma sugestão inválida (ID omitido) e registra como `failed` sem alterar o agrupamento determinístico; nunca envia nem altera o grupo fixado, enviando ao dublê somente a parte ainda ajustável da proposta; nunca chama o dublê quando há menos de duas ordens posicionáveis para regrupar; e descarta uma sugestão válida quando a proposta muda (simulado dentro do próprio dublê) enquanto a chamada estava em andamento, provando que nenhuma transação fica aberta durante essa chamada;
- imagem Docker completa (`api`, `worker`, `healthcheck`, `migrate`) reconstruída após essas mudanças: compilação aprovada; binário `api` iniciado contra o banco de teste real duas vezes — sem Workers AI configurado e com credenciais fictícias configuradas — em ambos os casos `/health/ready` respondeu corretamente e o segundo caso registrou o log de habilitação esperado;
- `npm run lint` e `npm run build` executados novamente após a mudança em `dashboard.tsx` (rótulo do `generator` no painel): aprovados.

Não foi executado teste contra o MK real nem mutação externa, nem qualquer chamada real ao Cloudflare Workers AI (as credenciais usadas nos testes e no smoke test acima são fictícias e nunca chegam a sair da rede de teste).

## Ponto exato de parada

- O fluxo local simulado cobre sincronização, listagem, localização manual, agrupamento determinístico, edição, fixação, atualização, aprovação e prévia de agendamento.
- Criar e recalcular uma proposta agora pedem uma sugestão ao Workers AI configurado e a aplicam se ela validar; sem configuração (padrão) ou em qualquer falha, o agrupamento determinístico já calculado é o que fica. Nenhum modelo Cloudflare real foi escolhido ou testado — a fronteira está pronta, mas ainda não foi exercitada contra a API real.
- A prévia de agendamento é somente leitura. Solicitações e envios não são criados enquanto a convenção diária de início/fim e o contrato real do MK não forem confirmados.
- O modo real permanece bloqueado pelos contratos externos listados neste documento; não existe fallback automático para dados simulados.
- Os testes PostgreSQL de concorrência de tarefas (reserva, lease, tentativas, atraso), de sincronização repetida/parcial e de sugestão de agrupamento (aplicação, descarte por invalidez, respeito a grupos fixados, corrida com edição concorrente) já existem e passam contra PostgreSQL/PostGIS real.

## Próximo marco recomendado

Escolher e testar um modelo real do Workers AI (a documentação da Cloudflare deve ser conferida antes de presumir que o JSON Schema é sempre respeitado) e então habilitar `CLOUDFLARE_ACCOUNT_ID`/`CLOUDFLARE_API_TOKEN`/`CLOUDFLARE_AI_MODEL` em um ambiente real para observar o comportamento de ponta a ponta pela primeira vez — até agora só um cliente substituto (dublê) foi exercitado. Em paralelo, decidir se o painel deve poder pedir uma nova sugestão sob demanda e avaliar se o laço do processo `worker` (não só a fila e a sincronização simulada isoladamente) merece um teste de retomada após reinício simulado. O envio real continua condicionado à definição dos horários diários e à confirmação dos contratos MK.
