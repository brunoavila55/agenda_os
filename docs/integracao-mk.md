# Estado da integração MK

## Confirmado

- autenticação `.rule`: `/mk/WSAutenticacao.rule`;
- catálogo de tipos: `/mk/WSMKOSListaTiposOS.rule`;
- o token de entrada e o token retornado são credenciais distintas;
- a API Node pode ter base/porta diferente da API `.rule`.

## Bloqueios para o modo real

1. código exato dos tipos rurais escolhidos;
2. endpoint e resposta anonimizados da listagem por tipo/situação;
3. base e habilitação da API Node;
4. retorno completo anonimizado de uma O.S. e códigos de situação/agendamento;
5. vínculo inequívoco com conexão/coordenadas, se existir;
6. associação e correção pelo MK dos serviços 38/39;
7. IDs reais de equipes, técnicos e agenda responsável;
8. convenção de início/fim do planejamento diário e confirmação do agendamento;
9. release, permissões, restrição de IP e limites do ambiente;
10. servidor, domínio, fuso definitivo, provedor geográfico e modelo Workers AI.

`APP_MODE=real` falha na inicialização do worker enquanto esses contratos não estiverem implementados. Não existe fallback para fixtures.

