# Arquitetura do primeiro marco

O frontend conversa apenas com sua rota BFF. O BFF valida a sessão e adiciona o token interno ao chamar a API. A API e o worker compartilham módulos de configuração e banco, mas são processos separados. O navegador nunca controla a frequência da sincronização.

O banco separa:

- `service_orders`: última observação conhecida do MK/simulador;
- `planning_proposals` e `proposal_items`: decisão local editável;
- `scheduling_requests` e `scheduling_attempts`: execução externa por item;
- `jobs`, `sync_runs` e `events`: trabalho, observabilidade e trilha de decisão.

Jobs são reservados com `FOR UPDATE SKIP LOCKED`; a transação termina antes do processamento. Um lease expirado pode ser recuperado. Mutações externas futuras deverão reconciliar antes de qualquer repetição.

