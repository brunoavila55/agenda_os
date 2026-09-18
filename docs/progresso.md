# Progresso do desenvolvimento

Atualizado em 17 de setembro de 2026.

Este documento registra o que foi confirmado no código atual. Ele não representa validação contra o ERP MK real.

## Resumo

O projeto possui o primeiro marco funcional em modo simulado: frontend autenticado, API Go, worker independente, PostgreSQL/PostGIS, fila persistente e painel de consulta. O modo real continua bloqueado deliberadamente enquanto os contratos pendentes do MK não forem confirmados.

| Etapa | Estado | Evidência atual |
| --- | --- | --- |
| 1. Estrutura, contratos e modo simulado | Concluída para o escopo inicial | API, worker, frontend, Compose, fixtures sintéticas e documentação de bloqueios |
| 2. Banco e tarefas | Parcial | Esquema inicial, jobs com lease e tabelas de propostas/comandos; faltam APIs, testes de persistência e mecanismo de migração de deploy |
| 3. Cliente MK e sincronização real | Não iniciada | Não existe adaptador MK; `APP_MODE=real` bloqueia o worker explicitamente |
| 4. Painel operacional | Parcial | Resumo, lista, filtros básicos, detalhes, configuração de tipos e situação da sincronização |
| 5. Localização e grupos calculados | Não iniciada | Coordenadas simuladas são armazenadas, mas o mapa é esquemático e não há cálculo ou edição de grupos |
| 6. IA | Não iniciada | Variáveis de ambiente previstas, sem cliente Workers AI ou validação de saída |
| 7. Agendamento | Não iniciado | Existem tabelas de solicitações e tentativas, sem fluxo, API ou chamada MK |
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
- aviso permanente de modo simulado e ausência de fallback silencioso para fixtures.

## Lacunas técnicas prioritárias

### Fundação e confiabilidade

- adicionar testes com PostgreSQL para sincronização repetida/parcial, concorrência, leases e reinício;
- registrar falhas de sincronização independentemente da transação que atualiza as ordens;
- usar o intervalo configurado por operação; atualmente o worker usa o `SYNC_INTERVAL` global;
- adicionar paginação à API de ordens, atualmente limitada a 500 registros;
- criar um mecanismo de aplicação de novas migrações durante o deploy;
- garantir isolamento dos dados por origem/modo também nas consultas do painel;
- ampliar testes de autenticação, autorização e proteção das rotas de escrita.

### Planejamento no simulador

- implementar APIs e regras para criar, editar, versionar e aprovar propostas diárias;
- detectar alterações concorrentes e mudanças no estado observado da ordem;
- permitir selecionar equipe e responsável da agenda sem confundir esses conceitos;
- criar grupos e pendências, mover ordens manualmente e preservar grupos fixados;
- preparar uma prévia do agendamento sem enviar dados ao MK.

### Localização e agrupamento

- substituir o mapa esquemático por uma visualização geográfica baseada nas coordenadas armazenadas;
- implementar correção manual de localização e invalidação por mudança de endereço;
- calcular proximidade no PostGIS com unidades e SRID adequados;
- criar agrupamento determinístico utilizável sem IA;
- manter ordens sem posição confiável visíveis para revisão.

### Integração MK

- criar o adaptador MK e DTOs separados dos modelos internos;
- implementar autenticação e cache seguro do token retornado;
- atualizar manualmente o catálogo de tipos;
- importar ordens elegíveis e continuar reconciliando ordens acompanhadas;
- diferenciar erro HTTP, funcional, autenticação, permissão e resposta malformada;
- implementar limites de concorrência, timeout e repetição apenas para leituras seguras.

### IA e agendamento

- integrar o Workers AI somente depois do agrupamento determinístico;
- minimizar os dados enviados e validar integralmente IDs, esquema e cobertura das ordens;
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

Executadas em 17 de setembro de 2026:

- `npm run lint`: aprovado;
- `npm run build`: aprovado com Next.js 16.3.3;
- compilação dos três binários Go na imagem Docker: aprovada;
- `go test ./...` dentro da imagem de build: aprovado;
- pacotes `internal/config` e `internal/httpapi`: quatro testes unitários aprovados;
- módulos de banco, sincronização e worker: ainda sem testes automatizados.

Não foi executado teste contra o MK real nem mutação externa.

## Próximo marco recomendado

Completar o planejamento diário no modo simulado, incluindo propostas versionadas, equipes, grupos editáveis, localização manual e agrupamento determinístico. Esse marco valida o fluxo operacional sem depender dos contratos externos e prepara as fronteiras necessárias para a futura integração MK.
