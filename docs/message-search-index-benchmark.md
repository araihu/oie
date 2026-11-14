# Protótipo de busca de mensagens com Bleve e FTS5

O protótipo compara três planos no endpoint real de busca de mensagens do Oiê. O
SQL cronológico percorre o índice de tempo até preencher a página. O FTS5 filtra
por trigramas seguros conforme a proposta anterior. O Bleve, com Scorch em
disco, combina trigramas literais e buscas fuzzy sobre prefixos normalizados.
Os três reusam o matcher, as regras de conta e sala, as revisões, o cursor e a
hidratação do SQLite. O código de produção extraiu a hidratação compartilhada
em um método interno, preservando a consulta e a conexão usadas no caminho
padrão. [Proposta FTS5](message-search-fts5-proposal.md)

## Planos e cobertura

- O FTS5 aplica `OR` aos trigramas separados de cada termo com tolerância a
  erros, e combina termos seguros com `AND`. Ele retorna IDs para o mesmo SQL
  de verificação usado com Bleve.
- O Bleve gera trigramas e prefixos de palavras normalizadas com `search.Fold` e `search.Words`.
  Para cada termo com fuzzy, consulta a união de um trigrama literal e os
  prefixos compatíveis com o limite de edição do Oiê. O Scorch habilita
  transposição no autômato. O mapeamento usa tokens keyword, sem ranking.
  Prefixos armazenados têm de 3 a 48 runas. O plano dispensa uma restrição fuzzy
  se o maior prefixo admissível passar desse teto. Trigramas cobrem também
  trechos literais no interior de palavras. [Scorch v2.6.1](https://github.com/blevesearch/bleve/blob/v2.6.1/index/scorch/snapshot_index.go)
- Os dois planos indexados verificam todos os candidatos em uma consulta SQL
  por IDs quando o conjunto tem até 1.024 IDs. A busca por candidatos pede
  uma linha extra para detectar que ultrapassou o teto. Consultas sem termo
  seguro, conjuntos amplos, falha do índice e salas conhecidas de até 512
  mensagens usam o SQL sem índice de texto. Com histórico habilitado, o plano
  sem índice de texto usa a consulta com busca de revisões por entidade. A
  heurística de tamanho das salas usa os tamanhos conhecidos da base de teste.

O índice de cada plano é uma projeção derivada, com uma entrada por mensagem
canônica e o conteúdo pesquisável das versões atuais e históricas. O
verificador SQL consulta cada versão separadamente. A base de desempenho é
imutável e o teste de paridade também exercita um índice desatualizado com IDs
pendentes fornecidos pelo teste. A leitura inclui esses IDs e descarta
correspondências obsoletas. Um worker ativo exigiria um protocolo que
coordene o snapshot do SQLite, a leitura do Bleve e a confirmação durável das
pendências. A confirmação só pode remover a pendência da versão efetivamente
publicada. Essa coordenação e o custo sob ingestão concorrente são trabalho de
uma implementação de produção.

## Reprodução

Executar na raiz do Oiê. O módulo alternativo fixa o Bleve v2.6.1 e mantém
a dependência fora do build padrão. As rotas experimentais são compiladas
apenas nos testes com a tag `searchprototype`.

```sh
GOWORK=off go test -modfile=tools/search-prototype.mod -tags=searchprototype \
  ./internal/adapters/sqlite -run '^TestMessageSearchPrototypeParity$' -count=1 -v

OIE_SEARCH_BENCH=1 GOWORK=off go test -modfile=tools/search-prototype.mod \
  -tags=searchprototype ./internal/adapters/sqlite \
  -run '^TestMessageSearchPrototypeBenchmark$' -count=1 -timeout=30m -v
```

As respostas completas tipadas da API são comparadas com a consulta atual do
engine, incluindo a ordem, os metadados e o cursor. O ensaio usa SQLite WAL e
Scorch persistidos em arquivos temporários. O SQLite executa `ANALYZE` após
a carga em lote, como no ensaio FTS5 anterior. As 100 mil mensagens reproduzem
os textos e tamanhos de sala do ensaio FTS5, com termos repetidos, uma revisão
por mensagem e `documento` a cada 1.777 mensagens. Uma conta contém 99.900
mensagens e a outra, 100. O teste menor cobre versões, acentos, Unicode,
símbolos, termos longos, prévias, escopos, empates de tempo e falha do índice.

Cada célula de latência é o p95 por ordem estatística de 40 pedidos aquecidos
e intercalados em Linux arm64 com Go 1.27.4 e SQLite 3.54.1. O host também
executava verificações do repositório. A medição vai do pedido HTTP em
loopback à resposta JSON decodificada. Inclui validação da API, consulta ao índice ou a decisão de
fallback, consulta e hidratação SQLite e serialização. A consulta atual serve
de oráculo de paridade. A medida de p95 compara os três planos propostos com
o mesmo pool de leitura do SQLite. O percurso no navegador, a consulta XMPP e
a latência com atividade de escrita simultânea exigem medições próprias.

## Latência e custo do índice

p95 em milissegundos. ↪ indica fallback para SQL cronológico.

| Consulta | Página | Itens | SQL cronológico | FTS5 + SQLite | Bleve + SQLite |
| --- | ---: | ---: | ---: | ---: | ---: |
| `arqiuvo` global | 1 | 50 | 352,0 | 11,5 | 11,1 |
| `arqiuvo` global | 2 | 50 | 755,5 | 15,3 | 18,6 |
| `arqiuvo`, sala de 100 | 1 | 50 | 39,3 | 42,3 ↪ | 41,9 ↪ |
| `arqiuvo`, sala de 99.900, sem resultado | 1 | 0 | 2.352,6 | 11,0 | 6,9 |
| `faturamento` global | 1 | 50 | 17,6 | 22,0 ↪ | 20,1 ↪ |
| `faturamento` global | 2 | 50 | 13,5 | 14,7 ↪ | 16,2 ↪ |
| `faturamento`, sala de 100, sem resultado | 1 | 0 | 1,5 | 3,0 ↪ | 3,8 ↪ |
| `viedo` global | 1 | 50 | 10,4 | 10,4 ↪ | 12,2 ↪ |
| `viedo` global | 2 | 50 | 8,4 | 12,1 ↪ | 9,4 ↪ |
| `viedo`, sala de 100, sem resultado | 1 | 0 | 2,6 | 1,7 ↪ | 1,8 ↪ |
| `docun` global | 1 | 50 | 1.898,6 | 1.478,8 ↪ | 33,8 |
| `docun` global | 2 | 6 | 118,7 | 118,3 ↪ | 7,7 |
| `docun`, conta com 99.900 mensagens | 1 | 50 | 968,8 | 903,2 ↪ | 16,9 |

A vantagem específica observada para Bleve foi a busca seletiva por um
prefixo curto com erro de digitação. Em `docun`, o FTS5 proposto não tem
trigramas suficientes para pré-filtrar com garantia de cobertura. O Bleve
encontrou os prefixos elegíveis sem percorrer a linha do tempo. O resultado
compara as implementações testadas. Um índice adicional de bigramas poderia
mudar a cobertura do FTS5 para termos curtos.

Para `arqiuvo`, os dois índices reduziram o p95 em relação ao SQL e se
alternaram na frente conforme a página. Na sala pequena e nas consultas
globais muito frequentes, o percurso cronológico preencheu a página com
pouco trabalho ou percorreu um escopo pequeno. A diferença entre colunas
quando todas usam esse mesmo plano inclui variação do host e, no caso de
termos frequentes, o custo de tentar o índice antes do fallback.

A preparação comum da projeção levou 1,03 s. A construção do FTS5 levou
1,13 s em uma transação. O acréscimo ao banco SQLite após o checkpoint foi
22,18 MiB, incluindo o mapa de IDs e escopos. A construção do Bleve levou
6,44 s com lotes de 1.000 e fechamento do índice. O diretório ocupou
18,36 MiB. São custos das representações testadas. O FTS5 desta base
estática usa `content=''` sem `contentless_delete`. Um índice atualizável
precisa de armazenamento externo do texto ou de opções compatíveis para
alterar e remover documentos, com novos ensaios de tamanho e escrita.

Os resultados sustentam o percurso cronológico como alternativa para
consultas comuns e salas pequenas. O Bleve merece um ensaio com carga real
se os prefixos curtos seletivos forem frequentes. A seleção para produção
depende do p95 com ingestão concorrente, custo de atualização e reconstrução
e confirmação do protocolo de snapshots e pendências.

## Achados dos reviews

- O controle cronológico agora usa um limite de tempo diretamente sobre o
  índice de expressão. A comparação completa da tupla do cursor ainda
  decide os empates. O `EXPLAIN QUERY PLAN` confirmou acesso por faixa tanto
  na busca global quanto na sala.
- A primeira execução sem estatísticas mostrou custo alto na hidratação de
  remetentes da sala grande. Em uma cópia da base, a consulta de identidade
  levou em média 58,8 ms antes do `ANALYZE` e 29,4 µs depois, em 20
  repetições por caso. O plano passou de percorrer as mensagens da
  sala para buscar a identidade pelo ID. Os resultados comparativos usam
  as mesmas estatísticas em todos os planos. Uma implantação precisa medir
  também bancos reais antes de assumir esse plano de hidratação.
- O Bleve só entrega candidatos quando retornou o conjunto completo dentro
  do orçamento. Resposta parcial, erro de expansão ou falha do índice
  acionam a consulta SQL completa. Corrigimos a combinação de opções do
  FTS5 e a simulação de índice indisponível. O fixture verifica sequência de
  páginas e cursores, escopo nos dois índices, metadados de múltiplas
  referências e o limite do campo de prefixos.

Os testes de paridade com a tag `searchprototype`, `make check` e
`make ci-check` via Dagger passaram. A revisão independente também verificou
3.697 variantes aceitas pelo matcher em um teste direto do Scorch com
prefixos, incluindo transposições, acentos e caracteres CJK.
