# Proposta de FTS5 para a busca de mensagens do Oiê

Usar o FTS5 para selecionar mensagens candidatas e manter `oie_search_match` como verificador. A proposta cobre as mensagens retidas na busca global e na busca dentro de uma conversa. Os demais grupos da busca global, o catálogo e a consulta ao arquivo XMPP seguem seus caminhos atuais. O objetivo é diminuir o trabalho por consulta, com paridade de resultados, versões, ordem e paginação. A proposta mantém duas rotas. O FTS atende consultas seletivas em escopos grandes. O percurso cronológico atende salas pequenas e consultas em que as correspondências são frequentes o suficiente para preencher a página cedo. [Busca atual](message-search.md) · [Especificação R08](oie-spec.md)

## Seleção sem perda de resultados

Indexar trigramas do texto normalizado por `search.Fold`, com `trigram case_sensitive 1`. O Go aplica a mesma normalização antes de extrair os termos de consulta. Usar cada trigrama como literal entre aspas, dobrar aspas internas e passar a expressão `MATCH` como parâmetro SQL. Como as expressões usam literais individuais de exatamente três runas e só precisam retornar IDs, avaliar `detail=none` e `columnsize=0` para reduzir o índice. [Normalização do Oiê](../internal/search/match.go) · [Tokenizer de trigramas](https://sqlite.org/fts5.html#the_trigram_tokenizer)

| Termo após normalização | Filtro necessário para entrar entre os candidatos |
| --- | --- |
| Termo de 3 ou mais runas sem tolerância a edições | Exigir um trigrama literal do termo. |
| Fuzzy de 7 runas, 1 edição | Buscar pelo menos um de 2 trigramas separados por uma runa. |
| Fuzzy de 11 ou mais runas, 2 edições | Buscar pelo menos um de 3 trigramas, com uma runa entre vizinhos. |
| Fuzzy de 4 a 6 ou 8 a 10 runas, termos literais curtos e símbolos isolados | O termo fica a cargo do verificador. |

Cada edição no alinhamento do matcher afeta no máximo um bloco escolhido. Inserir uma runa pode interromper o interior de um bloco. Remover ou substituir uma runa afeta o bloco que a contém. Inverter vizinhas consome duas runas adjacentes, e o espaço entre blocos impede que as duas pertençam a blocos distintos. Com um bloco a mais que o orçamento de edições, pelo menos um aparece intacto no prefixo aceito. A regra também cobre o caminho literal do matcher, já que todos os termos extraídos ocorrem na consulta inteira. As alternativas de um termo formam um `OR` completo. Restrições de termos diferentes podem ser combinadas com `AND`. [Matcher](../internal/adapters/sqlite/search_match.go) · [Distância de edição](../internal/search/match.go)

`arqiuvo` gera `"arq" OR "uvo"`. `arquivo` entra por `arq` e o Go aceita a inversão. `viedo faturamento` pode usar só os blocos de `faturamento`, que tem 11 runas. `viedo orçamento` fica na busca sem FTS porque `orcamento` tem nove runas e aceita duas edições. Consultas que geram zero termos, como pontuação isolada, também seguem a busca sem FTS. Caso os termos curtos dominem a carga medida, testar um índice separado de bigramas codificados como tokens. Dois bigramas separados precisam de cinco runas para uma edição. Três precisam de oito para duas edições. Medir o custo de gravação e o tamanho das listas de candidatos antes de adotar esse índice.

## Índice derivado e atualização

Manter inicialmente uma linha no FTS por mensagem canônica. Usar uma tabela de projeção com `INTEGER PRIMARY KEY` explícita para ligar o `rowid` do FTS ao UUID canônico. O `rowid` implícito de `messages` pode mudar em um `VACUUM`. Incluir o texto da versão atual, os metadados pesquisáveis disponíveis e os textos das versões históricas admitidas pela consulta. Separar as partes com espaço. Essa união pode selecionar uma mensagem cujos termos aparecem em versões distintas. O SQL atual verifica cada versão separadamente e descarta esse candidato. A consulta de origem continua determinando o corpo, a agregação dos metadados, a versão correspondente e o trecho mostrado. [Versões, metadados e filtros da consulta](../internal/adapters/sqlite/queries/message_search.sql) · [VACUUM no SQLite](https://sqlite.org/lang_vacuum.html)

Registrar IDs pendentes na mesma transação que altera a fonte, desde antes de começar o preenchimento inicial. Cobrir inserções e remoções de mensagens e revisões, mudanças de versão atual, fatos de edição, mudanças de raiz canônica e aliases antigos e novos, referências, versão do parser e mudanças de URL, nome de arquivo, título e descrição de prévia, inclusive quando um valor é removido. O worker lê a fonte, atualiza ou exclui o documento e limpa a pendência da mesma geração em uma transação. Se a preparação ocorrer fora dela, o worker só grava e limpa a pendência se sua geração ainda corresponder à alteração mais recente. [Precedente de projeção SQLite](../internal/adapters/sqlite/migrations/000027_shared_catalog_projection.up.sql)

Ativar a geração atomicamente após cobrir as mensagens existentes. Cada mensagem elegível precisa estar atualizada no índice ou coberta por uma pendência ou exceção persistente. Consultas indexadas leem esses IDs no mesmo snapshot do FTS e da fonte. Um documento com texto que o tokenizer não possa indexar por inteiro fica nesse conjunto persistente até poder ser indexado. Enquanto a geração estiver incompleta ou indisponível, a busca usa o plano sem FTS. Durante o preenchimento, limitar os lotes para liberar o escritor entre transações. [Consistência do FTS5](https://sqlite.org/fts5.html#external_content_table_pitfalls)

Testar o tokenizer do binário distribuído com entradas Unicode, aspas, caracteres de sintaxe de busca e NUL em fontes decodificadas de JSON. Substituir NUL por espaço no texto do índice após `Fold`, preservando a fonte para o verificador. Versionar a projeção quando mudarem o parser ou a normalização. Verificar suporte ao FTS5 nos alvos de distribuição antes de incluir a tabela em uma migração obrigatória. O rollback da otimização usa o plano sem FTS no mesmo binário. Voltar a um binário anterior exige tratar a versão do esquema pelos mecanismos de migração ou recuperação já existentes. [Migrações do Oiê](../internal/adapters/sqlite/migrate.go)

## Planos de consulta e custo

- **Busca sem FTS**. Substituir a agregação global de `initial_revisions` por buscas de primeira revisão por entidade no índice `message_revisions_entity`. Comparar filtros diretos para conta e sala e um plano que percorre a linha do tempo e para após confirmar os resultados da página. Em páginas seguintes, verificar no plano se o cursor vira um limite do índice cronológico. Essa rota atende consultas sem filtro seguro, índices indisponíveis e escopos em que a busca local custa menos. [Busca de primeira revisão por entidade](../internal/adapters/sqlite/queries/message_edit_history.sql)
- **Busca com FTS**. Partir da união deduplicada dos IDs do `MATCH`, das pendências e das exceções persistentes, buscar mensagens e suas revisões por chave e aplicar o escopo de conta, sala e visibilidade da consulta atual. Confirmar conteúdo e histórico, aplicar o cursor e ordenar antes de limitar os resultados. Inspecionar `EXPLAIN QUERY PLAN` para confirmar que o plano percorre os candidatos e acessa a mensagem e as revisões por índice. Medir se vale materializar os IDs uma vez para compartilhar a seleção entre a versão atual e o histórico. Um limite intermediário pode ajudar a escolher o plano se ultrapassá-lo acionar a busca completa. Truncar candidatos antes do verificador perde resultados. [Ordem e cursor atuais](../internal/adapters/sqlite/queries/message_search.sql)
- **Escolha do plano**. Calibrar com o tamanho do escopo e o custo dos termos. Uma lista global de trigramas comuns pode custar mais que percorrer uma sala pequena. Medir a decisão por consulta, incluindo o custo de estimar a seletividade. Uma sondagem limitada por candidatos ou pendências pode trocar para o plano sem FTS se passar do orçamento. Contar toda a lista para escolher o plano pode consumir o ganho. O plano cronológico é a primeira alternativa a testar com o histórico desativado. Usar o mesmo cursor para os dois planos, inclusive quando a escolha muda entre páginas. Medir se preparar a consulta normalizada uma vez por pedido acelera a rota sem FTS. Medir separadamente o efeito de mover a busca do escritor atual para o pool de leitura WAL, usando o mesmo modo de leitura para comparar os planos com e sem índice. [Conexões SQLite do Oiê](../internal/adapters/sqlite/store.go)

## Verificação antes de ativar

Comparar a consulta em produção, o SQL otimizado sem FTS e o plano com FTS no mesmo corpus. Medir p50 e p95 do endpoint com cache frio e aquecido, consultas seletivas e comuns, ausência de resultados, FTS indisponível e termos curtos, com primeira página e páginas profundas. Incluir busca global, filtros por conta e salas pequenas e grandes. Medir chamadas ao matcher, tempo de preenchimento, tamanho do banco e do WAL e p95 de gravação com ingestão e busca concorrentes.

Comparar páginas completas e cursores, incluindo IDs canônicos, revisão correspondente, marca histórica, tipo do match, trecho, prévia e cobertura de metadados. Exercitar texto e metadados na mesma busca, acentos, símbolos, combinações de edições, empates de horário, isolamento entre contas, mensagens ocultas, alterações de raiz, prévias e correções concorrentes com a busca, remoções, reabertura após `VACUUM`, interrupção e retomada do preenchimento e mudança de versão do parser. O teste deve alternar também entre os dois planos durante a paginação.

Os critérios propostos para ativação são paridade completa, redução de pelo menos 20% no p95 da carga representativa em relação à produção e redução de pelo menos 30% no p95 das buscas seletivas elegíveis em relação ao melhor plano sem FTS. Para consultas sem filtro seguro e salas pequenas, o p95 pode subir no máximo 5% sobre o melhor plano sem FTS. Esses valores são metas da proposta. Fixar a mistura de consultas, o tamanho das bases e os orçamentos de escrita e espaço antes dos ensaios. Validar primeiro a rota sem FTS, que já reduz o custo de salas pequenas. Em seguida, validar a projeção e comparar consultas indexadas em modo de validação. Ativar o FTS apenas nos escopos em que vencer o melhor plano sem índice pelos critérios acima.

## Ensaios do protótipo

O protótipo usa as migrações, o SQL gerado e o matcher em Go do Oiê em `6e6fe31`. A base sintética em memória tem 100 mil mensagens, uma conta, uma sala de 100 mensagens e outra de 99.900, com uma revisão por mensagem. O teste usa o SQLite 3.54.1 do driver `modernc.org/sqlite` v1.62.0. Compara as 15 colunas SQL da primeira página e da segunda, quando há cursor, com janela de 50 resultados e uma linha extra. A comparação passou em todos os cenários da tabela. As latências são medianas de cinco execuções aquecidas da primeira página em Linux arm64. [Protótipo reproduzível](prototypes/message-search-fts5/main.go)

```sh
go run ./docs/prototypes/message-search-fts5 -runs=5
```

| Consulta e escopo | SQL atual | Revisões por índice | Percurso cronológico sem FTS | FTS5 |
| --- | ---: | ---: | ---: | ---: |
| `arqiuvo`, global | 1.398 ms | 1.446 ms | 957 ms | 4,2 ms |
| `arqiuvo`, sala com 100 mensagens | 333 ms | 29,6 ms | 3,8 ms | 10,7 ms |
| `arqiuvo`, sala com 99.900 mensagens, sem resultado | 4.128 ms | 3.213 ms | 3.801 ms | 4,0 ms |
| `faturamento`, sala com 100 mensagens, sem resultado | 402 ms | 103 ms | 9,1 ms | 102 ms |
| `faturamento`, global, 24.900 correspondências | 5.505 ms | 11.565 ms | 22 ms | 14.697 ms |
| `viedo`, global, sem filtro seguro | 8.221 ms | 2.693 ms | 3,0 ms | — |
| `viedo`, sala com 100 mensagens, sem resultado | 143 ms | 15,8 ms | 1,3 ms | — |

O plano com FTS consultou os trigramas e acessou mensagens e revisões por chave. No SQL atual, o SQLite materializou a primeira revisão de cada mensagem mesmo na sala de 100 mensagens. O percurso cronológico foi mais rápido na sala pequena e nas consultas globais comuns que preencheram a página cedo. Nesta última, pôde parar nas primeiras 51 correspondências, enquanto o FTS precisou verificar e ordenar candidatos espalhados pelo histórico. O preenchimento do FTS5 em memória levou 1,23 s. A quantidade de resultados recentes também favoreceu o percurso cronológico em `viedo`, que segue elegível para pesquisa por aproximação.

Uma verificação adicional com o mesmo driver gerou 11.341 entradas com inserções, remoções, substituições e transposições, incluindo termos latinos e CJK. Os trigramas recuperaram as 5.517 entradas aceitas pelo matcher nessa amostra.

O ensaio de tempo mede a consulta SQL e usa textos repetidos. A projeção do protótipo contém apenas o corpo da mensagem e usa o `rowid` de uma base imutável. A verificação de ativação mede a hidratação de resultados, prévias e progresso do catálogo no engine, assim como os demais grupos da busca global e a renderização no navegador. Também cobre edições, metadados, índice persistente, custo da escolha do plano e escrita concorrente.
