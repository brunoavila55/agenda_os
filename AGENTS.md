# AGENTS.md — Painel de planejamento de O.S. MK Solutions

## 1. Finalidade deste arquivo

Este documento orienta agentes de desenvolvimento e colaboradores que trabalham neste repositório. Deve ficar na raiz do projeto e ser lido antes de implementar mudanças.

Descreve o produto pretendido, decisões de arquitetura, regras operacionais, contratos conhecidos e critérios de entrega. Não comprova que as funcionalidades já estejam implementadas. Verifique o código, as migrações, os testes e a documentação existente antes de afirmar que algo funciona.

As instruções explícitas atuais do responsável pelo projeto prevalecem sobre decisões de produto registradas aqui. Quando uma decisão mudar, atualize a documentação e preserve a distinção entre fato confirmado, proposta técnica e pendência.

## 2. Produto e objetivo

Construir uma aplicação web pessoal para consultar, organizar, planejar, agendar e acompanhar ordens de serviço do ERP MK Solutions.

O objetivo operacional é reunir manutenções próximas para facilitar o planejamento diário de uma equipe. O usuário revisa as sugestões e aciona o agendamento. O ERP continua sendo a fonte oficial dos dados operacionais.

O sistema não tem intenção de monetização. Não implementar cobrança, assinaturas, planos comerciais, marketplace, cadastro público ou estrutura SaaS multiempresa sem solicitação expressa. As operações rural e fibra pertencem ao mesmo contexto operacional e não são tenants comerciais.

## 3. Contexto confirmado pelo usuário

| Tema | Definição |
| --- | --- |
| Operação inicial | Manutenção rural |
| Volume inicial | Entre 3 e 5 O.S. por dia |
| Equipe inicial | Dois técnicos trabalhando juntos na mesma equipe |
| Expansão futura | Fibra óptica, aproximadamente 100 O.S. por dia e 20 técnicos |
| Ordens para planejamento | Somente abertas e sem agendamento |
| Sincronização | A cada 10 minutos, independentemente de o painel estar aberto |
| Planejamento | Diário; não foi definida uma grade de duração por atendimento |
| Técnicos no MK | Usuários que possuem agenda de O.S.; não representam disponibilidade horária ou GPS |
| Encerramento | O técnico costuma encerrar todas as ordens ao fim do dia |
| Localização disponível | Endereço obtido na consulta da O.S.; coordenadas não estão confirmadas |
| Infraestrutura | Servidor novo; especificação e domínio ainda não definidos |
| IA | Modelo hospedado no Cloudflare Workers AI, restrito ao agrupamento |
| Ambiente de testes do usuário | CachyOS e Postman |

Não interpretar encerramentos simultâneos como atendimentos simultâneos. Não deduzir duração real, produtividade por hora, posição ou atendimento em execução a partir do horário de encerramento.

Não dividir automaticamente os dois técnicos rurais em equipes independentes. O responsável da agenda MK e os integrantes da equipe são conceitos distintos.

## 4. Fluxo funcional esperado

1. Consultar os tipos de O.S. e permitir selecionar os códigos que pertencem à manutenção rural.
2. Persistir a seleção por ID; disponibilizar atualização manual do catálogo de tipos.
3. A cada 10 minutos, consultar as ordens elegíveis e atualizar a cópia local.
4. Obter os detalhes necessários e resolver a localização de cada ordem.
5. Calcular proximidade e gerar uma proposta de agrupamento, com auxílio limitado da IA.
6. Exibir lista, mapa, pendências e grupos para revisão manual.
7. Permitir escolher o dia, a equipe e a agenda responsável dentre os cadastros do MK.
8. Preparar uma prévia dos agendamentos e solicitar a ação explícita do usuário no painel.
9. Persistir a solicitação, revalidar os dados e enviar ao MK.
10. Confirmar o resultado por leitura e continuar acompanhando a situação da ordem.

Planejar uma vez por dia e sincronizar a cada 10 minutos são frequências diferentes. A sincronização não deve refazer automaticamente um planejamento aprovado.

## 5. Fonte oficial e separação de estados

Manter três camadas independentes:

| Camada | Conteúdo | Regra |
| --- | --- | --- |
| Estado observado no MK | Situação, responsável, agendamento, endereço e demais dados consultados | Registrar origem e instante da consulta |
| Proposta local | Grupos, escolhas do usuário e planejamento ainda não enviado | Pode ser editada sem alterar o ERP |
| Execução | Solicitação, tentativas, resultado e confirmação posterior | Não confundir envio com sucesso |

Uma proposta local nunca significa que a ordem foi agendada. Um HTTP 200 isolado também não significa sucesso funcional.

Alterações feitas diretamente no MK prevalecem no estado observado. Se forem incompatíveis com uma proposta, sinalizar conflito e exigir revisão antes do envio.

Ordens já acompanhadas continuam sendo consultadas mesmo após deixarem de aparecer na listagem de elegíveis. Ausência em uma resposta não prova encerramento, cancelamento ou exclusão.

## 6. Integração MK: evidências e limitações

### 6.1 Referências oficiais

- APIs gerais: https://mkloud.atlassian.net/wiki/spaces/MK30/pages/48699908/APIs+gerais
- APIs especiais: https://mkloud.atlassian.net/wiki/spaces/MK30/pages/48699991/APIs+especiais

Conferir os contratos e a release instalada antes de implementar chamadas reais. Algumas respostas da documentação são imagens; exemplos reais anonimizados devem fundamentar o mapeamento final.

### 6.2 Contratos identificados

| Função | Caminho documentado | Situação conhecida |
| --- | --- | --- |
| Autenticar | `/mk/WSAutenticacao.rule` | Testado pelo usuário com sucesso; confirmado de novo em 2026-09-18 pelo cliente Go em `backend/internal/mk/client.go` contra `sac.newlifefibra.com.br` |
| Listar tipos | `/mk/WSMKOSListaTiposOS.rule` | Testado pelo usuário com sucesso; confirmado de novo em 2026-09-18 pelo cliente Go, formato de resposta real igual ao documentado em §6.4 |
| Listar equipes | `/mk/WSMKOSListaGruposServico.rule` | Documentado; funcionamento não confirmado |
| Listar técnicos responsáveis | `/mk/WSMKOSListaTecnicoResponsavel.rule` | Documentado; funcionamento não confirmado |
| Consultar O.S. por ID | `GET /os/id` | API Node; endereço-base não confirmado |
| Listar O.S. por cliente | `GET /os/pessoa` | Documentado; não substitui a listagem por tipo |
| Agendar | `/mk/WSMKAgendamentoOrdem.rule` | Documentado; contrato do ambiente não testado |
| Cancelar/reagendar | `/mk/WSMKCancelarReagendarOrdem.rule` | Documentado; fora do envio inicial do MVP |
| Listar todas as O.S. por tipo | Não confirmado | Bloqueador da sincronização real completa |

Não inventar um endpoint de listagem por tipo. Não identificar códigos de tipo, técnico, equipe ou situação pelo número de outro cadastro.

### 6.3 Autenticação

A autenticação documentada recebe `sys`, `token`, `password` e `cd_servico`. O token de entrada é a credencial do usuário; o token retornado é utilizado nas consultas seguintes. Não confundir os dois.

`sys` é sempre `"MK0"` — confirmado fixo em duas instalações MK reais e distintas do usuário (não apenas esta). Não expor como variável de ambiente configurável; hardcoded em `backend/internal/mk/client.go`.

O retorno observado contém campos como `Token`, `Expire`, `LimiteUso`, `ServicosAutorizados` e `status`. Não presumir fuso de `Expire` ou semântica de `LimiteUso` sem confirmar. Respeitar perfis com uso único e serviços efetivamente autorizados.

`cd_servico=9999` não concede permissões ausentes no perfil. Não usar esse valor como solução genérica para falhas. **Confirmado em 2026-09-18**: com `cd_servico=9999`, `ServicosAutorizados` devolveu a lista completa de códigos que o perfil de Webservice pode efetivamente usar — e o token resultante funcionou para `WSMKOSListaTiposOS.rule`. O que determina o que uma chamada pode fazer é essa lista, não o valor de `cd_servico` passado na autenticação em si.

O nome do campo do token retornado varia entre instalações reais do MK (confirmado contra dois sistemas distintos do usuário — `Token` direto em um, aninhado sob outro nome em outro). `backend/internal/mk/client.go` busca recursivamente por `token`/`tokenautenticacao`/`tokenretornoautenticacao` ignorando caixa, em vez de assumir um único nome de campo.

### 6.4 Tipos de O.S.

Formato **confirmado contra o MK real em produção em 2026-09-18** (`sac.newlifefibra.com.br`, via `cd_servico=9999`), não mais ilustrativo:

```json
{
  "Tipos": [
    { "codostipo": 93, "descricao": "BAIXA SETOR RURAL" },
    { "codostipo": 29, "descricao": "INSTALAÇÃO RURAL" }
  ],
  "status": "OK"
}
```

Devolve o catálogo completo (~130 tipos) numa única chamada, sem paginação. `codostipo` vem como número JSON (não string).

Candidatos identificados no catálogo real com "RURAL" na descrição — falta a escolha final do usuário sobre qual(is) representa(m) a operação de manutenção rural do produto:

| Código | Descrição |
| --- | --- |
| 29 | INSTALAÇÃO RURAL |
| 30 | VISITA TÉCNICA - RURAL |
| 93 | BAIXA SETOR RURAL |
| 226 | VISADA + INSTALAÇÃO RURAL |
| 249 | MANUTENÇÃO RURAL POP |

Buscar o nome ajuda na configuração inicial; depois persistir o ID. Não usar correspondência textual aproximada para decidir elegibilidade a cada ciclo.

O `id` de `/os/id` é o código de uma ordem específica, não `codostipo`.

### 6.5 Problemas observados

- Dois retornos 500 indicaram violação de `mk_ws_consumo_cd_servico_fkey`: os códigos 38 e 39 não estavam presentes em `mk_ws_servicos`.
- **Confirmado em 2026-09-18**: os códigos `38` e `39` também não aparecem em `ServicosAutorizados` na resposta de `WSAutenticacao.rule` para este perfil — a causa não é uma falha transitória do MK, é uma permissão que falta no cadastro do perfil de Webservice. Se esses serviços forem realmente necessários, é preciso pedir ao suporte/comercial MK para liberá-los no perfil, não corrigir nada do nosso lado.
- A associação de cada código a seu endpoint/função de negócio ainda não foi confirmada. Não afirmar qual consulta corresponde a 38 ou 39.
- Não corrigir o banco interno do MK diretamente nem criar registros de serviço por suposição.
- A tentativa `/mk//os/id` retornou 404 HTML. Isso não demonstra inexistência da ordem nem indisponibilidade de toda a API Node.
- Configurar bases independentes para endpoints `.rule` e API Node, se necessário. Não concatenar `/mk/` indiscriminadamente.

### 6.6 Agendamento diário ainda pendente

A API documentada de agendamento exige `codigoOS`, `tecnico`, `data_hora_inicio`, `data_hora_fim` e token; `CodAgendaGrupo` é opcional na documentação consultada.

O usuário trabalha com planejamento diário. Confirmar como esse fluxo deve ser representado no MK antes de enviar em produção: janela diária, convenção da agenda ou horários definidos pelo operador.

Não preencher um dia inteiro, dividir o turno ou inventar duração sem decisão explícita. Validar formato de data, fuso, regras de sobreposição e correspondência entre agenda, colaborador e equipe.

## 7. Repositórios de referência

| Repositório | Uso previsto |
| --- | --- |
| https://github.com/brunoavila55/api-mk-octadesk | Principal referência: cliente Go do MK, cache de token, timeout, sanitização de erros e Workers AI com JSON estruturado |
| https://github.com/brunoavila55/mk-consulta-conexao | Consulta de conexões; modelo inclui endereço, latitude e longitude, sem provar preenchimento no ambiente |
| https://github.com/brunoavila55/rotas-newlife | Referência de Go, Next.js e Compose; versão inspecionada é conversor de contatos com dashboard, não motor geográfico |

Ler os arquivos atuais antes de reutilizar. Não copiar credenciais, configurações de produção ou regras de negócio de atendimento Octadesk. Preservar autoria/licença aplicável. Não modificar os repositórios de referência para implementar este produto.

## 8. Arquitetura de referência

Adotar um monólito modular, com API e worker executados em processos separados e compartilhando os módulos de domínio. Implantar inicialmente no mesmo servidor.

| Componente | Responsabilidade |
| --- | --- |
| Next.js + TypeScript | Interface, autenticação/sessão conforme estratégia escolhida e comunicação com a API |
| Go API | Regras de negócio, consultas locais, propostas e comandos autenticados |
| Go worker | Sincronização, tarefas de localização, agrupamento e reconciliação |
| PostgreSQL | Dados persistentes, controle de concorrência e tarefas |
| PostGIS | Operações espaciais e agrupamento calculado |
| Workers AI | Sugestões de agrupamento via chamada do backend |
| Docker Compose | Execução e implantação no servidor próprio |

Não introduzir Kubernetes, broker dedicado, Redis, banco vetorial ou framework de agentes sem uma necessidade demonstrada. A meta de fibra exige controle de dados e concorrência; não obriga uma arquitetura distribuída complexa.

Separar DTOs do MK, modelos internos e contratos HTTP do painel. Toda chamada MK deve passar pelo adaptador MK; componentes de interface e módulo de IA não acessam o ERP diretamente.

### 8.1 Organização sugerida

- `frontend/`: aplicação Next.js.
- `backend/cmd/api/`: processo HTTP.
- `backend/cmd/worker/`: processo de tarefas.
- `backend/internal/config/`: configuração e validação inicial.
- `backend/internal/mk/`: contratos e cliente do ERP.
- `backend/internal/sync/`: sincronização e reconciliação.
- `backend/internal/planning/`: operações, propostas e grupos.
- `backend/internal/geo/`: localização e proximidade.
- `backend/internal/llm/`: cliente Workers AI e validação.
- `backend/internal/scheduling/`: envio e confirmação.
- `backend/internal/jobs/`: tarefas persistidas.
- `backend/internal/httpapi/`: API do painel e autorização.
- `backend/internal/store/`: acesso ao banco.
- `migrations/`: evolução do esquema.
- `docs/`: contratos MK, decisões e operação.
- `compose.yaml`, `.env.example`, `README.md`.

Essa árvore é uma proposta para projeto novo. Respeitar convenções equivalentes se o repositório já tiver organização coerente.

## 9. Operações rural e fibra

Representar rural e fibra como configurações de operação. Evitar regras espalhadas no código condicionadas ao nome de uma operação.

Cada operação deve permitir definir tipos MK elegíveis, equipes/agenda responsáveis, frequência de consulta, critérios geográficos e configurações de planejamento. Prioridades e duração estimada podem existir como extensões, mas seus valores não devem ser inventados.

A operação fibra futura pode ter equipes com composição diferente da rural. Não fixar dois integrantes globalmente. A pertença de um mesmo tipo ou ordem a múltiplas operações exige regra explícita para evitar processamento duplicado.

## 10. Modelo de dados e invariantes

Entidades esperadas: operações, tipos MK, técnicos, equipes, vínculos de equipe, ordens, localizações, propostas diárias, grupos, integrantes dos grupos, solicitações de agendamento, tentativas, tarefas, execuções de sincronização e eventos.

Regras:

- IDs locais são distintos de IDs externos. Escolher representação interna após validar tipos e limites dos códigos externos.
- Garantir unicidade da ordem por origem MK e ID externo.
- Separar código/situação original do ERP da interpretação normalizada. Código desconhecido exige revisão; não assumir que está aberto.
- Somente aberta e sem agendamento é elegível para nova proposta. Detalhe insuficiente não significa ausência de agendamento.
- Preservar origem, versão e instante de observação dos dados relevantes.
- Uma ordem aparece no máximo uma vez em uma proposta ativa aplicável, inclusive entre grupos e pendências.
- Uma ordem não pode ter dois comandos ativos de agendamento independentes.
- Revisão de proposta deve detectar edição concorrente e dados MK alterados.
- Guardar eventos de decisão e execução sem registrar indiscriminadamente payloads pessoais.
- Usar migrações versionadas. Não modificar migração já aplicada para disfarçar uma alteração de esquema.
- Instantes são armazenados com fuso adequado; data operacional é calculada no fuso explicitamente configurado.
- Não apagar histórico ao atualizar a cópia local da ordem.

## 11. Sincronização e tarefas

- Intervalo inicial: 10 minutos. O navegador não controla o agendador.
- Sincronização manual utiliza o mesmo mecanismo da automática.
- Evitar sobreposição por operação/origem e impedir comandos concorrentes sobre a mesma ordem.
- Paginar ou aplicar filtros incrementais somente quando o contrato da API os confirmar.
- Limitar chamadas simultâneas, aplicar timeout e espera progressiva com variação para leituras que possam ser repetidas.
- Não repetir automaticamente mutações só porque o protocolo usado é GET. Classificar chamadas pelo efeito de negócio.
- Reutilizar detalhes quando seguro; invalidar cache quando os campos relevantes mudarem.
- Diferenciar erro HTTP, autenticação, permissão, erro funcional, resposta malformada e indisponibilidade.
- Marcar uma execução como completa apenas após todas as páginas/partes esperadas. Falha parcial não autoriza remoção ou encerramento local.
- Mostrar dados desatualizados e último sucesso, preservando a consulta do painel durante falhas do MK.
- Ordens enviadas, com resultado incerto ou ainda acompanhadas exigem reconciliação independente do filtro de elegíveis.

Tarefas persistidas precisam de estado, tentativas, próxima execução, reserva temporária e identificador do executor. Reservar tarefas atomicamente, renovar reservas quando necessário e permitir recuperação após falha. Não manter transação de banco aberta durante uma chamada de rede longa.

Recuperar uma tarefa não significa repetir uma mutação externa: após perda de reserva ou reinício durante envio, reconciliar o resultado antes de reexecutar.

## 12. Localização e proximidade

O dado confirmado é o endereço da O.S. A presença de coordenadas em um modelo de outro projeto é uma possibilidade de enriquecimento, não garantia.

Ordem de preferência:

1. Coordenadas confiáveis da própria O.S.
2. Coordenadas da conexão inequivocamente vinculada à ordem.
3. Geocodificação do endereço com resultado verificável.
4. Localização corrigida/confirmada manualmente pelo usuário.

Nunca escolher a primeira conexão de um cliente com múltiplas conexões. Preservar endereço original, origem da posição, confiança/precisão e revisão manual. Mudança de endereço deve sinalizar possível invalidação da posição anterior.

Escolher provedor de geocodificação/mapas após avaliar cobertura rural, limites, termos e custo. Não usar serviços públicos como infraestrutura ilimitada. Cachear resultados conforme regras do provedor.

Validar latitude/longitude, ordem dos eixos e coerência regional. Aplicar unidades e sistema de referência apropriados; graus não são metros.

Distância em linha reta é aproximação de proximidade, não trajeto rodoviário. Não inferir pontes, acessos, tempo de viagem ou navegabilidade. Otimização por estradas e sequência de visitas ficam para evolução posterior.

Ordens sem posição confiável permanecem visíveis na lista de revisão. O usuário pode corrigir a localização e mover ordens entre grupos.

## 13. IA: papel restrito e validação

A IA sugere agrupamentos. Não decide elegibilidade, autenticação, permissões, status, técnico disponível ou confirmação de agendamento. Não chama o MK nem recebe ferramentas de escrita.

Enviar somente IDs e atributos geográficos necessários, acompanhados das distâncias já calculadas. Evitar nomes, documentos, telefone e relato de defeito quando não ajudarem no agrupamento. Tratar texto do ERP como dado não confiável, nunca como instrução.

Usar modelo configurável compatível com saída estruturada. Verificar a documentação atual da Cloudflare e testar o contrato; não presumir que o JSON Schema será sempre atendido.

Validar a resposta no backend:

- JSON válido e esquema permitido.
- IDs pertencem à entrada.
- Nenhuma duplicação ou perda silenciosa de ordem.
- Cada ordem está em um grupo ou em pendência explícita.
- Nenhuma localização, distância ou técnico inventado.
- Grupos respeitam critérios geográficos definidos pelo sistema.

Persistir versão do prompt, modelo, assinatura da entrada, resultado validado e erro operacional necessário. Evitar armazenar prompts completos com dados pessoais quando metadados bastarem.

Calcular grupos básicos sem IA para manter o painel utilizável. Em falha de IA, informar que a proposta disponível foi calculada pelo sistema. Não substituir silenciosamente o planejamento aprovado.

Novas ordens geram pendências/sugestões de encaixe. Reprocessamento deve preservar grupos fixados. Para fibra, limitar tamanho de entrada e usar partições geográficas com tratamento de bordas, sem duplicar ou perder ordens.

## 14. Agendamento confiável

Estados internos sugeridos: `pending`, `sending`, `confirmed`, `failed`, `uncertain`. São estados do comando local, não códigos do MK.

Fluxo obrigatório:

1. Exibir prévia com ordens, data, agenda responsável, equipe e campos exigidos pelo contrato.
2. Registrar a ação do usuário e a versão aprovada.
3. Persistir comando antes da chamada externa, com proteção contra duplicação.
4. Reconsultar a ordem e comparar situação/atributos relevantes.
5. Impedir envio de ordem encerrada, já agendada ou alterada de forma incompatível.
6. Executar a chamada pelo adaptador MK.
7. Interpretar sucesso funcional e confirmar os dados por leitura.
8. Atualizar o comando e o histórico individualmente.

Timeout, conexão interrompida ou reinício após envio podem significar que o MK executou a ação. Nesses casos, usar `uncertain`, consultar o ERP e não reenviar automaticamente. Se a evidência for insuficiente, manter revisão manual visível.

Não prometer execução exatamente uma vez sem suporte externo. A proteção local reduz duplicações, mas não elimina a janela entre a gravação no MK e a confirmação local.

Em lote, uma ordem pode ser confirmada e outra falhar. Preservar resultados por item; não declarar transação atômica entre sistemas nem cancelar automaticamente os sucessos.

Listagem de técnicos não permite verificar conflitos de horário. Se consulta de agenda não estiver disponível, explicar a limitação na prévia sem inventar validação. Verificar também a janela de concorrência com alterações externas; reconsulta não equivale a bloqueio no MK.

Não agendar duas vezes apenas porque a equipe rural possui dois integrantes. Confirmar quem representa a agenda responsável. Reagendamento/cancelamento exigem funcionalidade e intenção específicas.

## 15. Interface e experiência

Interface em português brasileiro, clara para uso diário. Incluir:

- Visão geral com pendentes, agendadas, encerradas e falhas.
- Lista filtrável por operação, situação, data, grupo e responsável.
- Detalhes com endereço, defeito e dados operacionais disponíveis.
- Mapa com legenda, grupos e pendências de localização.
- Planejamento diário editável, com seleção de equipe e agenda.
- Prévia de envio e resultado por ordem.
- Histórico de ações e indicação de confirmação pendente.
- Configurações de tipos e intervalo de sincronização.

Consultar a base local ao abrir o painel. Não disparar toda a cadeia MK em cada renderização. Atualizações visuais não podem sobrescrever edição em andamento sem aviso.

Desabilitar ações inválidas com explicação. Não mostrar GPS, técnico livre, ETA ou progresso de campo sem dados que os sustentem. Manter visível a diferença entre rascunho, sugestão e confirmado.

## 16. Segurança e privacidade

- Autenticação obrigatória para o painel e autorização também no backend; sem cadastro público.
- Sessões/cookies devem seguir a estratégia escolhida com proteção adequada, inclusive contra CSRF quando aplicável.
- Segredos MK e Cloudflare somente no servidor; nunca em variáveis públicas do Next.js, código cliente, fixtures ou commits.
- Usar HTTPS e validar certificados. Não desabilitar TLS como correção de produção.
- Sanitizar URLs e erros, pois APIs MK transportam segredos na query string. Evitar logs de request completos no app e no proxy.
- Validar destinos de integração e não aceitar URL arbitrária enviada pelo navegador para realizar chamadas autenticadas.
- Usar respostas anonimizadas em testes/documentação e contas/perfis com permissões necessárias.
- Tokens exibidos anteriormente em capturas não devem ser reutilizados ou transcritos. Não presumir que a rotação já ocorreu.
- Definir retenção para histórico e dados pessoais; limitar acesso a backups e logs.
- Não executar agendamentos reais durante testes automáticos.

## 17. Simulação e bloqueios de integração

Disponibilizar modo simulado explícito e modo real. Não alternar automaticamente para dados fictícios em caso de falha real.

O modo simulado deve mostrar aviso persistente, usar IDs/dados sintéticos e nunca enviar ao ERP. Isolar seus dados dos reais.

Quando faltar contrato, implementar interface e testes com fixtures, registrar a pendência e bloquear a chamada real com erro legível. Continuar o trabalho independente dessa pendência; não alegar conclusão da integração.

Pendências atuais:

1. Código exato dos tipos rurais escolhidos.
2. Endpoint e resposta da listagem de ordens por tipo/situação.
3. Base, porta e habilitação da API Node.
4. Retorno completo de uma O.S. e códigos de situação/agendamento.
5. Disponibilidade de vínculo de conexão e coordenadas.
6. Endpoints associados aos erros de serviços 38/39 e correção pelo MK.
7. Cadastros reais de equipe, técnicos e agenda responsável.
8. Convenção de início/fim para planejamento diário e semântica de confirmação.
9. Release MK, permissões, restrição de IP e limites de consumo.
10. Servidor, domínio, fuso, provedor geográfico e modelo de IA definitivo.

## 18. Testes e critérios de qualidade

Priorizar testes que protegem regras e efeitos externos:

- Parsing de contratos reais anonimizados e rejeição de campos essenciais ausentes.
- Token expirado/uso único, erro funcional com HTTP 200, HTML inesperado e resposta truncada.
- Sincronização repetida, parcial, concorrente e retomada após reinício.
- Elegibilidade: aberta sem agenda, encerrada, agendada e situação desconhecida.
- Alteração direta no MK entre proposta e envio.
- Duplo clique, lote parcialmente concluído e timeout após possível sucesso externo.
- Recuperação de tarefa sem duplicar mutação.
- Localização inválida, endereço ambíguo, unidades geográficas e múltiplas conexões.
- IA com JSON inválido, IDs inventados, duplicados ou omitidos.
- Autenticação e autorização nas rotas de leitura e escrita.
- Fluxo integrado: importar, revisar, agrupar, aprovar, enviar e reconciliar.

Testes de carga usam MK simulado. Avaliar o volume de novas ordens e o estoque acumulado de abertas; 100 novas/dia não significa só 100 linhas no banco.

Medir duração de sincronização, consultas externas, latência da interface, fila e latência/custo da IA. Definir metas com base no ambiente, sem prometer números não medidos.

Não ampliar dependências ou suíte de testes sem resolver um risco concreto. Nunca apresentar testes simulados como validação contra o ERP real.

## 19. Implantação e observabilidade

Servidor novo com Compose, aplicação e banco persistentes. Banco sem porta pública, acesso web com HTTPS e segredos injetados externamente.

Health checks devem distinguir processo vivo, prontidão local e falha da integração externa. MK indisponível não deve reiniciar o sistema em loop nem impedir consulta dos dados locais.

Registrar duração, resultado e correlação de tarefas sem segredos. Exibir última sincronização bem-sucedida, falhas consecutivas, comandos incertos e tarefas paradas.

Configurar backups fora do servidor e testar restauração. Documentar atualização, migrações e recuperação; um backup no mesmo disco não cobre perda do servidor.

Fixar versões compatíveis no projeto e usar lockfile no frontend. Não escolher recursos do servidor com base em estimativas apresentadas como benchmark.

## 20. Roteiro e critérios de entrega

| Etapa | Entrega | Critério de conclusão |
| --- | --- | --- |
| 1 | Estrutura, contratos e modo simulado | API, worker e frontend sobem; pendências documentadas |
| 2 | Banco e tarefas | Migrações aplicam; reinício não perde propostas/comandos |
| 3 | Cliente MK e sincronização | Contratos confirmados funcionam; parcial não corrompe estado |
| 4 | Painel operacional | Lista, filtros, detalhes e situação da sincronização disponíveis |
| 5 | Localização e grupos calculados | Dados incertos vão para revisão; grupos são ajustáveis |
| 6 | IA | Saída validada, falha tratada e propostas aprovadas preservadas |
| 7 | Agendamento | Revalidação, envio e reconciliação por item sem reenvio cego |
| 8 | Piloto rural | Ciclo acompanhado com autorização, falhas e recuperação verificadas |
| 9 | Implantação e fibra | Restauração testada, operação configurável e carga sintética medida |

O primeiro marco é um painel persistente e útil nas etapas 1–4. A integração real completa depende da resolução dos contratos pendentes. Fibra deve ser adicionada por configuração e ajustes fundamentados nas medições.

## 21. Como trabalhar neste repositório

Antes de editar:

1. Leia este arquivo, o README e instruções específicas do diretório.
2. Verifique o estado do Git e preserve alterações existentes do usuário.
3. Identifique o que já está implementado e a etapa solicitada.
4. Confira contratos, migrações, scripts e testes disponíveis.

Durante a implementação:

- Faça mudanças coesas para a etapa pedida, evitando reescrita ampla sem motivo.
- Não crie contratos MK por plausibilidade; registre a lacuna e trabalhe no adaptador simulado.
- Atualize `.env.example` sem valores reais e documente novas configurações.
- Centralize validação e regras de negócio no backend.
- Passe contexto/cancelamento nas chamadas Go, limite recursos e formate o código.
- Use TypeScript com tipos explícitos nas fronteiras; valide dados recebidos em runtime.
- Preserve trilha de decisão para estados externos e migrações.
- Não execute mutações no MK de produção para testar uma hipótese.
- Não considere este arquivo autorização para publicar, alterar o ERP, rotacionar credenciais ou modificar repositórios de referência.

Antes de entregar:

- Rode formatação, compilação e testes relevantes usando os comandos existentes.
- Para Go, use `gofmt` e `go test ./...` no módulo quando aplicável.
- Para frontend, use o gerenciador indicado pelo lockfile e os scripts existentes; não invente comandos executados.
- Verifique mudanças de banco e a documentação correspondente.
- Informe o que mudou, verificações realizadas, limitações e próximo bloqueio concreto.

Mantenha este documento atualizado quando decisões operacionais ou contratos forem confirmados. Os detalhes técnicos extensos de cada endpoint devem evoluir em `docs/`, com exemplos anonimizados e indicação de origem.
