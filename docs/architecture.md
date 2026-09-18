# Arquitetura do primeiro marco

O frontend conversa apenas com sua rota BFF. O BFF valida a sessão e adiciona o token interno ao chamar a API. A API e o worker compartilham módulos de configuração e banco, mas são processos separados. O navegador nunca controla a frequência da sincronização.

O banco separa:

- `service_orders`: última observação conhecida do MK/simulador;
- `planning_proposals` e `proposal_items`: decisão local editável;
- `scheduling_requests` e `scheduling_attempts`: execução externa por item;
- `jobs`, `sync_runs` e `events`: trabalho, observabilidade e trilha de decisão.

Jobs são reservados com `FOR UPDATE SKIP LOCKED`; a transação termina antes do processamento. Um lease expirado pode ser recuperado. Mutações externas futuras deverão reconciliar antes de qualquer repetição.

O planejamento determinístico usa `ST_DWithin` sobre `geography`, portanto o raio configurado é interpretado em metros. O backend transforma os pares próximos em componentes conexos; ordens sem posição ficam em pendência explícita. Edições usam a versão da proposta como controle otimista e a aprovação revalida versão observada, situação aberta e ausência de agendamento.

O recálculo preserva os integrantes ainda elegíveis de grupos marcados como fixos e recompõe apenas o restante. Localizações possuem versão independente da observação da ordem. Uma correção manual é preservada durante sincronizações; mudança posterior do endereço mantém a posição, mas a marca para revisão e impede aprovação silenciosa.

A prévia de agendamento é somente leitura. Ela revalida estado, versões e localização por ordem, apresenta equipe e agenda responsável e mantém a criação de comandos bloqueada enquanto não houver convenção de horários e contrato MK confirmado.

A fronteira de Workers AI usa o endpoint REST oficial e solicita saída por JSON Schema. O validador local exige cobertura exata dos IDs, rejeita duplicações e invenções, impede agrupar ordens sem posição e verifica conectividade usando somente as distâncias calculadas pelo sistema. Prompt completo e dados pessoais não são persistidos; a tabela de sugestões guarda modelo, versão do prompt, assinatura da entrada e resultado validado ou erro sanitizado.

Criar ou recalcular uma proposta sempre grava primeiro o agrupamento determinístico, em uma única transação, como já fazia antes da IA existir. Só depois — fora de qualquer transação — o backend pode pedir uma sugestão ao cliente Workers AI configurado (`store.GroupingSuggester`, uma interface substituível por um dublê nos testes; `nil` quando não configurado) sobre a parte ainda ajustável da proposta: ordens posicionadas que estão pendentes ou em um grupo não fixado. A resposta é validada de novo no backend independentemente do que o cliente já validou. Se ela passar, uma segunda transação curta aplica a sugestão somente se a proposta ainda estiver exatamente na versão de onde a entrada foi construída; caso contrário — ou em qualquer erro de rede, validação ou timeout — a sugestão é descartada e o agrupamento determinístico já persistido permanece como resposta. Grupos fixados e propostas aprovadas nunca entram nessa chamada. Nenhuma transação de banco fica aberta durante a chamada de rede ao Workers AI.

O serviço `migrate` aplica arquivos SQL numerados antes da API e do worker. Cada migração é registrada com checksum e executada em transação sob trava consultiva. Os dois primeiros esquemas, originalmente aplicados apenas no bootstrap do PostgreSQL, são reconhecidos para adoção segura por volumes existentes.
