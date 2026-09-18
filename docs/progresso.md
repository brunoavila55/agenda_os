# Progresso do desenvolvimento

Atualizado em 18 de setembro de 2026.

Este documento registra o que foi confirmado no código atual. Ele não representa validação contra o ERP MK real.

## Resumo

O projeto possui o primeiro marco funcional em modo simulado: frontend autenticado, API Go, worker independente, PostgreSQL/PostGIS, fila persistente e painel de consulta. O modo real continua bloqueado deliberadamente enquanto os contratos pendentes do MK não forem confirmados.

| Etapa | Estado | Evidência atual |
| --- | --- | --- |
| 1. Estrutura, contratos e modo simulado | Concluída para o escopo inicial | API, worker, frontend, Compose, fixtures sintéticas e documentação de bloqueios |
| 2. Banco e tarefas | Parcial avançada | Esquema, migrações de deploy com checksum, jobs com lease e propostas persistentes; ainda faltam testes PostgreSQL automatizados mais amplos |
| 3. Cliente MK e sincronização real | Não iniciada | Não existe adaptador MK; `APP_MODE=real` bloqueia o worker explicitamente |
| 4. Painel operacional | Parcial avançada | Resumo, lista paginada, busca, detalhes, tipos, sincronização e planejamento diário editável |
| 5. Localização e grupos calculados | Parcial avançada | Agrupamento em metros, grupos fixados, recálculo, correção manual e visualização por coordenadas; provedor de mapa viário ainda não definido |
| 6. IA | Fundação concluída | Cliente Workers AI com JSON Schema, assinatura de entrada, validação geográfica e persistência de metadados; acionamento pelo planejamento ainda não conectado |
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

## Lacunas técnicas prioritárias

### Fundação e confiabilidade

- adicionar testes com PostgreSQL para sincronização repetida/parcial, concorrência, leases e reinício;
- evoluir o token interno único para papéis distintos caso novos usuários ou perfis sejam adicionados.

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

- conectar o cliente Workers AI ao planejamento somente após escolher e testar o modelo definitivo;
- construir a entrada apenas com IDs, coordenadas e distâncias calculadas, usando a validação já implementada;
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
- sincronização e worker ainda não possuem testes unitários próprios;
- `scripts/integration-test.sh`: aprovado em ambiente Compose descartável;
- teste integrado com PostgreSQL/PostGIS: migrações `001`–`006`, sincronização simulada, grupos fixados, nova ordem, recálculo, correção manual, invalidação por endereço, conflito `409`, aprovação, prévia bloqueada, busca e paginação;
- todas as rotas da API verificadas contra acesso sem token e parser JSON testado contra campos/documentos extras;
- adoção e segunda execução idempotente do executor de migrações: aprovadas;
- falha forçada de sincronização: registrada como `failed` após rollback da atualização de ordens.

Não foi executado teste contra o MK real nem mutação externa.

## Ponto exato de parada

- O fluxo local simulado cobre sincronização, listagem, localização manual, agrupamento determinístico, edição, fixação, atualização, aprovação e prévia de agendamento.
- A fundação do Workers AI está implementada e testada isoladamente, mas ainda não participa do planejamento. Nenhum modelo definitivo foi selecionado e nenhuma chamada à Cloudflare ocorre no fluxo normal.
- A prévia de agendamento é somente leitura. Solicitações e envios não são criados enquanto a convenção diária de início/fim e o contrato real do MK não forem confirmados.
- O modo real permanece bloqueado pelos contratos externos listados neste documento; não existe fallback automático para dados simulados.
- O próximo trabalho que independe desses contratos é ampliar os testes PostgreSQL de concorrência, leases e retomada de tarefas e preparar a orquestração da IA com cliente substituível em testes.

## Próximo marco recomendado

Ampliar os testes PostgreSQL de concorrência, especialmente reserva/expiração de tarefas, perda de lease e retomada após reinício. Depois, conectar a fronteira do Workers AI ao planejamento com um cliente substituível em testes, preservando o agrupamento determinístico como fallback e sem alterar propostas aprovadas ou grupos fixados. O envio real continua condicionado à definição dos horários diários e à confirmação dos contratos MK.
