# Governança de destino comercial CONFENGE

**Campanha:** `CONFENGE_MV_CAMPAIGN=08`

**Estado de decisão:** `EXECUTE_NOW`

**Frente executiva:** `REVENUE NOW`

**Tempo até evidência:** imediato no teste determinístico e após integração no primeiro first-touch novo; resultado comercial continua `UNKNOWN` até conversa e oportunidade qualificadas observadas.

**Alavancas:** receita, distribuição, automação e confiança.

O objetivo é manter continuidade entre o problema citado no primeiro contato e a prova pública que o destinatário encontra. A métrica corporativa continua sendo oportunidade comercial qualificada, não clique, mensagem, página ou quantidade de destinos.

## Estado observado em 5 de setembro de 2026

- Warmbly `origin/main` e produção estavam em `33bd329437bc04a2e95ef0f4d562d26b85f34e35`; CI e build desse SHA estavam verdes.
- Produção reportou backend, worker, consumer, Postgres, Redis, NATS, SMTP e IMAP saudáveis. O dispatch permanecia `PAUSED`, o auto-send legado e o GREEN autorun permaneciam desligados. O autorun delegado estava ativo, mas subordinado ao pause e aos gates finais.
- O feed factual estava `STALE`, com autoridade expirada e último sync parcial. Esta campanha não corrige nem contorna esse estado.
- `web-cfg` `origin/main` e `confenge.com.br` estavam em `89b081a8676d8a0b30747dfcb1477f21d9ac4dfb`.
- As oito rotas B2G usadas como destinos ativos responderam `200`, declararam canonical para `confenge.com.br` e expuseram a âncora registrada quando aplicável.
- A home de produção ainda era B2G, mas o roteamento não depende dessa hipótese. Uma home corporativa mais ampla pode entrar depois sem absorver tráfego de um claim específico.

## Auditoria do first-touch atual

| Componente | Estado encontrado |
| --- | --- |
| Spine | Feed versionado, target-fit, prova de fornecedor, candidate route, copy determinística, QA, aprovação hash-bound, fila canônica e gate final de transporte. |
| Template ativo | `delegated_first_touch_copy.go`, com prática derivada do `service_code`, refinamento por `moment_code` e CTA conforme pessoa, área ou caixa genérica. |
| Outros compositores | `cohort_compose.go` e `strategy_compose.go` preservam fluxos manuais/revisados. Não eram o autorun delegado observado em produção. |
| Segmento ativo | Empresas privadas confirmadas como contratadas em engenharia no setor público. Não é uma lista para novas verticais. |
| CTA | Pergunta de ownership/encaminhamento. Não pede reunião automaticamente e contém saída de contato. |
| URL no corpo | Nenhuma URL pública específica antes desta campanha. A assinatura continha apenas o endereço de e-mail no domínio CONFENGE. |
| Home | Nenhum link explícito para `/`, mas o domínio da assinatura podia levar o leitor à home por navegação manual. |
| UTM | Ausente antes desta campanha. |
| Dispatch | `PAUSED` em produção; `CURRENT_VERDICT=NO_GO_SMTP` era a última evidência registrada na issue Warmbly #43. |

O contrato `copy-rules.v1` continua aceito para mensagens já seladas. `copy-rules.v2` mantém assunto, abertura, prática, CTA, opt-out e assinatura do v1 e acrescenta uma única linha: “Veja como tratamos esse tipo de situação: URL”. O corpo, a URL e o destino entram no mesmo hash determinístico. Mensagem antiga não é reescrita silenciosamente.

## Mapa ativo de message match B2G

| Segmento/claim | Situação do visitante | Landing canônica e âncora | Prova pública esperada | Ação terminal |
| --- | --- | --- | --- | --- |
| Aditivo ou extra | Escopo/execução mudou e precisa de formalização | `/aditivos-obras-publicas/#metodo` | Método, documentos e limites de aditivo | Captura ou conversa humana na própria superfície |
| Medição ou glosa | Medição, memória, glosa ou recebimento em discussão | `/medicoes-glosas-obras-publicas/#metodo` | Critério de medição e prova contemporânea | Registrar demanda ou iniciar conversa humana |
| Reajuste ou reequilíbrio | Escolher mecanismo e organizar nexo/cálculo | `/reequilibrio-obras-publicas/#metodo` | Diferença entre mecanismos e método do pleito | Registrar contexto ou iniciar conversa humana |
| Orçamento ou BDI | Edital/planilha pode comprometer preço antes da proposta | `/auditoria-orcamento-licitacao/#metodo` | Auditoria de orçamento, BDI, SINAPI e SICRO | Registrar o edital ou conversar |
| Edital ou proposta | Decidir se vale disputar e organizar a proposta | `/bid-room-licitacoes-obras/#quando-nao-contratar` | Critério de GO/NO-GO e limites da operação | Captura do contexto do edital |
| Atraso, prorrogação ou encerramento | Prazo exige cronologia e posição | `/atrasos-prorrogacao-obras-publicas/#metodo` | Avisos, prova e decisão temporal | Captura da situação contratual |
| Carteira ou rotina | Priorizar eventos e responsáveis | `/acompanhamento-contratos-obras/#metodo` | Método e rotina de acompanhamento | Captura da necessidade de acompanhamento |
| Mercado público ou recorte PNCP | Acompanha oportunidades e recortes, sem edital ou disputa confirmada | `/problemas-que-resolvemos/` | Mapa de problemas, sem afirmar que há proposta em preparação | Escolher a situação específica |
| Contrato sem dor confirmada | Há atuação B2G, mas nenhuma dor factual autorizada | `/problemas-que-resolvemos/` | Mapa de problemas, sem inventar especialidade | Escolher a situação específica |

Todos os URLs recebem somente `utm_source=warmbly`, um `utm_medium` finito, um `utm_campaign` finito e `utm_content=<stable_landing_id>`. O código não recebe nome, e-mail, telefone, CNPJ, contrato, processo, texto da mensagem ou identificador do lead para construir o URL.

## Verticais preparadas, não ativadas

| Vertical | Stable landing id | URL proposta para decisão web | Estado |
| --- | --- | --- | --- |
| Empresas de engenharia/construtoras privadas | `private-engineering` | `/engenharia-empresas-privadas/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| Escritórios/plataformas de projetos | `project-offices` | `/escritorios-plataformas-projetos/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| Condomínios/administradoras | `condominiums-admin` | `/engenharia-condominios/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| Perícia/advogados | `expertise-law` | `/pericia-assistencia-tecnica/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| Avaliações | `valuations` | `/avaliacoes-engenharia/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| SST | `occupational-safety` | `/seguranca-saude-trabalho/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |
| Entes públicos | `public-entities` | `/engenharia-entes-publicos/` | `NOT_ACTIVATED`, `WITHHELD_PENDING_WEB_RELEASE` |

Esses caminhos são propostas para decisão do owner web, não páginas existentes nem autorização de campanha. O builder retorna erro e URL vazia para todos eles. Ativação futura exige, no mínimo, landing aprovada em `web-cfg` main e produção, prova pública adequada, fonte/consentimento/política da vertical e autorização comercial contemporânea. Esta campanha não cria lead list nem amplia SMTP.

## Mapa simples para operador humano

Isto é orientação de destino, não script de telemarketing:

| Se a conversa é sobre | Envie |
| --- | --- |
| Aditivo ou serviço extra | `https://confenge.com.br/aditivos-obras-publicas/#metodo` |
| Medição, glosa ou pagamento | `https://confenge.com.br/medicoes-glosas-obras-publicas/#metodo` |
| Reajuste ou reequilíbrio | `https://confenge.com.br/reequilibrio-obras-publicas/#metodo` |
| Orçamento, BDI ou planilha do edital | `https://confenge.com.br/auditoria-orcamento-licitacao/#metodo` |
| Edital e proposta | `https://confenge.com.br/bid-room-licitacoes-obras/#quando-nao-contratar` |
| Atraso, prorrogação ou encerramento | `https://confenge.com.br/atrasos-prorrogacao-obras-publicas/#metodo` |
| Carteira e acompanhamento contratual | `https://confenge.com.br/acompanhamento-contratos-obras/#metodo` |
| Inteligência de mercado ou recorte do PNCP | `https://confenge.com.br/problemas-que-resolvemos/` |
| Problema ainda indefinido em contrato público | `https://confenge.com.br/problemas-que-resolvemos/` |
| Qualquer nova vertical acima | Não envie landing. Registre o assunto para revisão; continua `NOT_ACTIVATED`. |

O operador não deve acrescentar PII, número de processo, contrato ou texto personalizado ao URL. Inbound, resposta ou conversa não autorizam outbound/SMTP automaticamente.

## Repetição, monitoramento e rollback

Cem repetições melhoram o sistema quando o mesmo registry evita cem decisões manuais de URL, mantém atribuição comparável e impede vazamento de PII. Cem mensagens para uma vertical sem landing/política seriam apenas cem unidades de risco e permanecem bloqueadas.

Monitorar por `stable_landing_id`, campaign kind e outcomes comerciais observados. Clique é sinal de distribuição, não oportunidade qualificada nem causalidade. Rollback de código restaura o compositor anterior; mensagens v1 já seladas não dependem do v2. Para retirar um destino sem reescrever mensagens históricas, mudar seu estado para `WITHHELD_PENDING_WEB_RELEASE`; novas composições falham fechadas.

`extra-cli` não foi alterado. O mapeamento usa somente a taxonomia `service_code`/`moment_code` já consumida por Warmbly e rotas públicas verificadas no owner `web-cfg`.

## Duas decisões que o código não toma por conveniência

`INTELIGENCIA_PNCP` não compartilha destino com `APOIO_LICITACAO`. Acompanhar
oportunidades no PNCP não é preparar proposta: enviar o recipiente para a Bid
Room afirmaria uma disputa que pode não existir. O caso tem claim próprio
(`MERCADO_PUBLICO_OU_RECORTE_PNCP`) e vai para a superfície de visão geral, que
já está publicada e ativa. A situação B2G está confirmada; a dor específica não.

A seleção por vertical não decide por ordem de array. B2G tem várias landings, e
devolver a primeira entregava aditivos a qualquer chamador que pedisse apenas a
vertical. `CommercialDestinationForVertical` agora falha fechado com
`ErrCommercialDestinationAmbiguous` quando a vertical tem mais de um destino: a
escolha comercial pertence à situação (serviço/momento) ou ao id explícito.
