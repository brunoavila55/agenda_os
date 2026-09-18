# Estado da integração MK

## Confirmado

- autenticação `.rule`: `/mk/WSAutenticacao.rule` — **testada contra o MK real de produção em 2026-09-18** (`sac.newlifefibra.com.br`). Contrato: `GET` com `sys=MK0`, `token` (token de entrada fixo), `password` (contrassenha do perfil), `cd_servico` na query string; nunca corpo JSON. Resposta real: `{"Expire":"...", "LimiteUso":0, "ServicosAutorizados":[...], "Token":"...", "status":"OK"}`. `sys` é sempre `"MK0"` — não é configurável, confirmado em duas instalações MK reais distintas do usuário;
- `ServicosAutorizados` (lista de códigos `cd_servico` inteiros) confirma que os códigos **38 e 39 realmente não estão autorizados** neste perfil — não é um problema transitório do MK, é uma permissão que falta no cadastro do perfil de Webservice;
- catálogo de tipos: `/mk/WSMKOSListaTiposOS.rule` — **testado contra o MK real em 2026-09-18** com `cd_servico=9999`. Contrato: `GET` com `sys=MK0` e `token` (o token temporário retornado pela autenticação). Resposta real: `{"Tipos":[{"codostipo":<int>,"descricao":<string>}, ...], "status":"OK"}` — bate exatamente com o formato que já estava documentado como ilustrativo. Devolve o catálogo completo (~130 tipos) numa chamada só, sem paginação;
- candidatos identificados no catálogo real para "manutenção rural" (ver `AGENTS.md` §6.4 para a lista completa) — falta a escolha final do usuário entre eles: `29` (INSTALAÇÃO RURAL), `30` (VISITA TÉCNICA - RURAL), `93` (BAIXA SETOR RURAL), `226` (VISADA + INSTALAÇÃO RURAL), `249` (MANUTENÇÃO RURAL POP);
- implementado em `backend/internal/mk/client.go`: `Client.Token` (cache por `cd_servico`, reautentica em 401/403), `Client.ListServiceTypes`, erros nunca incluem a URL nem credenciais (um `*url.Error` nativo do Go já vazou um token em log de produção antes, em outro projeto do usuário — ver comentário no código);
- o token de entrada e o token retornado são credenciais distintas;
- a API Node pode ter base/porta diferente da API `.rule`.

## Bloqueios para o modo real

1. ~~código exato dos tipos rurais escolhidos~~ → candidatos já identificados (ver acima); falta só a escolha final;
2. endpoint e resposta anonimizados da listagem de **O.S.** por tipo/situação (diferente do catálogo de tipos, que já está confirmado) — continua sem contrato conhecido, é o maior bloqueador da sincronização real;
3. base e habilitação da API Node;
4. retorno completo anonimizado de uma O.S. e códigos de situação/agendamento;
5. vínculo inequívoco com conexão/coordenadas, se existir;
6. ~~associação e correção pelo MK dos serviços 38/39~~ → confirmado que não estão em `ServicosAutorizados` deste perfil; se forem necessários, precisa pedir ao MK para liberá-los no perfil, não é um bug a corrigir do nosso lado;
7. IDs reais de equipes, técnicos e agenda responsável;
8. convenção de início/fim do planejamento diário e confirmação do agendamento;
9. release, permissões, restrição de IP e limites do ambiente;
10. servidor, domínio, fuso definitivo e provedor geográfico (o modelo Workers AI já foi escolhido e testado, ver `docs/progresso.md`).

`APP_MODE=real` falha na inicialização do worker enquanto esses contratos não estiverem implementados. Não existe fallback para fixtures.

