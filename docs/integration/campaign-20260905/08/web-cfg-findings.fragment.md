# Fragmento para MV-09: message match e landings

## Não editar automaticamente

Este arquivo é um achado para o integrador MV-09. Os caminhos abaixo não existem em `web-cfg` `origin/main` no BASE auditado (`89b081a8676d8a0b30747dfcb1477f21d9ac4dfb`). Eles não estão autorizados para publicação nem para campanha:

- `/engenharia-empresas-privadas/`
- `/escritorios-plataformas-projetos/`
- `/engenharia-condominios/`
- `/pericia-assistencia-tecnica/`
- `/avaliacoes-engenharia/`
- `/seguranca-saude-trabalho/`
- `/engenharia-entes-publicos/`

O registry Warmbly marca todos como `NOT_ACTIVATED` e `WITHHELD_PENDING_WEB_RELEASE`, portanto nenhum URL é emitido.

## Decisão pedida a MV-09

Para cada vertical, escolher explicitamente `MIGRATE`, `CREATE/VALIDATE`, `DEFER` ou `RETIRE` no nível do URL. Se a decisão for criar/validar, o owner web deve confirmar ou substituir o slug proposto e cumprir o registry público fail-closed: visitor job, perfil, ação terminal, prova, canonical, qualidade editorial/dados, monitoramento e rollback. O Warmbly só pode mudar o estado depois de a rota aprovada estar em `web-cfg` main e responder na produção canônica.

Também verificar que qualquer home corporativa ampla mantenha pontes para os destinos B2G já ativos. MV-08 não depende da home para os first touches de aditivo, medição/glosa, reajuste/reequilíbrio, orçamento/BDI, edital, atraso ou carteira.

## Evidência já verificada

As rotas B2G registradas como ativas estavam em `web-cfg` main, respondiam `200` em `confenge.com.br` e tinham canonical para o mesmo domínio. Nenhuma rota SmartLic, handoff público ou runtime paralelo foi introduzido.
