# Changelog — Concorde

Histórico do que foi feito em cada sessão, com detalhes técnicos. Sessões novas
leem este arquivo + o `Plano.md` antes de começar. Mais recente no topo.

---

## Sessão 19 — Contas de transmissor e senhas só como hash (2026-09-29)

Branch: `main`. Passo 4 do `Plano.md`, com as decisões conversadas antes (ver
"Decisões"). Backend (Go, pacote novo) + CLI + painel. Testado em Chrome real.
**Exige uma mudança no teu `.env` antes do próximo deploy** — ver "O que você
precisa fazer".

**O que muda para quem usa.** Amigos podem abrir a própria sala: você cria uma
conta para cada um no painel, eles entram com usuário e senha na página
principal e viram quem transmite naquela sala. O painel de admin continua **só
seu** — conta de transmissor leva 403 ali. Nenhuma senha fica guardada em lugar
nenhum: só o hash.

**Decisões (conversadas antes de escrever código):**
- **Cada um cria a própria sala**, não passar o bastão dentro da mesma sala. A
  segunda é uma feature bem mais cara (o papel vira mutável, a estrela do WebRTC
  inverte, e a regra "Sair de quem transmite fecha a sala" da Sessão 18 teria de
  ser repensada) — fica para se fizer falta.
- **`accounts.json`**, não SQLite: dez linhas de dados que mudam uma vez por mês
  não justificam cgo — que, aliás, **não compila nesta máquina** (só há gcc
  32-bit, como a Sessão 18 descobriu ao tentar `-race`).
- **bcrypt** (`golang.org/x/crypto`), custo 12 — medido em **263 ms** por
  verificação aqui.
- **A senha do admin vira `ADMIN_PASSWORD_HASH` no `.env`**, no mesmo lugar em
  que já morava. Põe o admin fora do arquivo de contas de propósito: perder ou
  corromper o `accounts.json` nunca pode trancar você fora do painel que
  gerencia o `accounts.json`. Sem bootstrap, sem rota de resgate extra.

**Pacote novo `internal/accounts`.** `Store` sobre um JSON, com `Verify`, `Add`,
`SetPassword`, `Remove`, `List`.
- **Gravação atômica:** escreve `accounts.json.tmp` e renomeia por cima, com
  permissão `0600` — uma escrita interrompida nunca deixa meia lista de contas.
- **Nomes tolerantes:** minúsculas, sem espaços nas pontas, `[a-z0-9._-]{1,32}`.
  "Ana " e "ana" são a mesma pessoa, não duas.
- **Regras de senha:** mínimo 10 caracteres; **máximo 72 bytes**, que não é regra
  nossa — o bcrypt ignora em silêncio o que passa disso, e senha truncada sem
  avisar é pior que senha recusada.
- **Arquivo que não dá para confiar para o servidor**: JSON inválido, conta sem
  hash, nome fora das regras ou `"role":"admin"` (o admin vem do ambiente; um
  arquivo que diz o contrário foi editado à mão ou adulterado). Subir como se
  ninguém tivesse conta trancaria todo mundo em silêncio.
- **Cache de verificação (5 min, esvaziado a cada mudança).** O painel
  reautentica a cada requisição e faz três a cada 5 s; sem o cache, 263 ms × 3 a
  cada 5 s manteriam um núcleo ocupado à toa — e o destino disto é um Raspberry
  Pi. Trocar ou apagar uma senha esvazia o cache, então vale na hora.
- **Tempo igual para nome que existe e nome que não existe:** quando o nome não
  existe, compara contra um hash falso fixo. Sem isso, o tempo de resposta diria
  quem tem conta.

**Servidor.** `adminCredentialsOK` (comparação fixa com o `.env`) saiu; entrou
`verifyLogin`, que consulta as contas e cobra o erro ao limitador de tentativas
da Sessão 16 — **fora do `h.mu`**, porque bcrypt é lento de propósito e o hub tem
um lock só para todas as salas. `createRoom` aceita qualquer conta; o painel
(`authorizeAdmin`) exige papel `admin` e responde **403** a uma conta de
transmissor — senha certa não é chute, então não conta para o bloqueio. Código de
erro `bad-admin` virou `bad-credentials`. A sala agora guarda o **dono**
(`Room.Owner`), que aparece no log (`[room] X created by ana (id)`) e na coluna
"De quem" do painel.

**Arranque (três caminhos, nenhum deles com senha em claro):**
1. `ADMIN_PASSWORD_HASH` presente → usa; valida que é mesmo um hash bcrypt.
2. `ADMIN_PASSWORD` (o formato antigo) ainda presente → **recusa subir**, dizendo
   exatamente o que fazer. Aceitar em silêncio desfaria o motivo de ter hash.
3. Nada configurado → **inventa uma senha forte para aquela execução** e imprime
   no log. Nunca existe credencial padrão conhecida, e clonar o repo e rodar
   continua funcionando.

**CLI (`cmd/server/cli.go`).** `concorde hash` (gera a linha do `.env`),
`concorde account list|add <nome>|passwd <nome>|rm <nome>`, `concorde help`. A
senha é pedida **sem eco** (`golang.org/x/term`) e confirmada duas vezes — nunca
vai como argumento (histórico do shell) nem aparece na tela, o que importa numa
máquina que vive compartilhando tela. Com stdin vindo de pipe, lê a linha normal
(script); um `bufio.Reader` só para o processo, porque um por prompt engoliria a
segunda linha.

**Painel.** Seção "Quem pode transmitir": lista, cria (**senha gerada pelo
servidor**, 16 caracteres, mostrada uma única vez — melhor que uma inventada na
hora), troca senha e remove. O admin do `.env` aparece marcado e sem botões. As
recusas voltam com **código estável** (`account-exists`, `bad-name`,
`short-password`…) e o painel escreve o motivo em português, como o resto do
site.

**Verificado.** `go build`/`vet`/`test ./...` limpos, gofmt conferido (sem CR).
Testes novos: `internal/accounts` (9) — ida e volta pelo arquivo, senha nunca em
claro nele, `.tmp` não fica para trás, nomes insensíveis a maiúsculas, regras de
senha, cache invalidado ao trocar/remover, admin do ambiente nem gravado nem
editável, e os seis arquivos ruins recusados; `internal/signaling` (6) — conta de
transmissor abre sala e a sala registra o dono, conta de transmissor leva 403 no
painel sem ser cobrada no bloqueio, CRUD da API, **hash nunca aparece na
listagem**, admin embutido não pode ser trocado nem removido, entrada inválida
vira 400 com código.

**Smoke test do fluxo real** (binário, `.env`, porta 3999): `concorde hash` →
`.env` → servidor sobe; admin 200 / conta de transmissor 403 / senha errada 401;
`account add` grava só o hash; senha curta recusada; "ANA " (maiúsculo, com
espaço) abre sala como `ana`; e os três caminhos de arranque acima, incluindo a
recusa do `ADMIN_PASSWORD` antigo e a senha inventada.

**Chrome real:** painel **8/8** (login, admin do `.env` sem botões, criar conta e
ver a senha uma vez, a senha mostrada realmente funcionar, trocar senha invalidar
a anterior na hora, nome inválido explicando a regra em português, remover, e o
arquivo final sem senha em claro). E a suíte da Sessão 18 rodou de novo inteira,
**22/22**, contra a autenticação nova.

> **Harness (scratchpad, fora do repo):** ganhou `prepareRun`, que monta um
> diretório de execução com `.env` próprio (com hash de verdade) e cópia do
> `public/`. Rodar a partir da pasta do repo não serve mais: o `.env` real tem
> `ADMIN_PASSWORD` e o servidor — corretamente — se recusa a subir.

**Não verificado aqui:** nada em produção ainda. E a carga do bcrypt no Pi é
estimativa: medi 263 ms nesta máquina, lá será bem mais — é por isso que o cache
existe.

**O que você precisa fazer (antes do próximo deploy):** ver o passo 1 do
`Plano.md`.

---

## Sessão 18 — Quedas: reconexão automática e retomada da sessão (2026-09-26)

Branch: `main`. Passo 3 do `Plano.md`. Backend (Go) + frontend (JS/CSS).
Testado em Chrome real (headless, ver "Verificado"). Sem custo novo: nada de
storage, nada de TURN, nada de serviço externo.

**A ideia.** O socket de sinalização **não é a transmissão**: o vídeo vai
peer-to-peer e não liga para o WebSocket ter morrido. Então a queda do socket
deixou de derrubar a sala. O **assento** (o lugar da pessoa na sala) passa a
sobreviver à conexão: quando o socket cai, o servidor segura o assento, a página
reconecta sozinha e **retoma a mesma sessão** — mesmo id, mesma sala, mesmo
papel — sem que os outros percebam nada. O keepalive da Sessão 14 continua
evitando a queda; isto aqui é a rede de segurança para quando ela acontece
mesmo.

**Assento e token (`internal/signaling/resume.go`, novo).**
- Ao entrar numa sala (criando ou entrando) o cliente recebe um `token` de 24
  caracteres (~120 bits) no `room-created`/`joined`. É credencial só daquele
  assento: vive no `sessionStorage` da aba, **nunca** a senha da sala, é revogado
  assim que o assento é entregue e morre junto da sala. Como não há o que
  adivinhar em 120 bits, retomada recusada **não** conta para o bloqueio de
  login da Sessão 16.
- `Hub.dropped` roda no lugar do `leaveRoom` quando o socket fecha: marca o
  cliente como `away` e o mantém em `room.clients`. **Ninguém é avisado** — nem
  `peer-left`, nem `broadcast-stopped` —, então o P2P de quem assiste segue
  tocando. `Client.away` é atômico porque o `enqueue` o lê de qualquer goroutine:
  sem write loop vivo, mensagem para quem está ausente é descartada em silêncio
  (nada se perde: a retomada responde com o estado inteiro da sala).
- `Hub.resumeSession` devolve o assento: `resumed {roomId, selfId, role, token,
  peers}`. A tomada é **incondicional**, mesmo que o socket antigo ainda pareça
  vivo — o navegador percebe o link morto muito antes do nosso keepalive, então a
  página costuma voltar antes de o servidor ter enterrado a conexão anterior; a
  velha é fechada ali (fora do lock). O token **rotaciona** a cada retomada.
- `Hub.expireAway` (via `time.AfterFunc`, com `recoverPanic`) libera o assento
  quando a carência acaba — aí sim sai o `peer-left` de sempre.
- `RESUME_GRACE_SECONDS` (default **30**; `≤0` desliga tudo e volta o
  comportamento anterior, inclusive sem emitir token).

**Quem transmite é caso à parte.** A janela dele **não** são 30 s: é o **TTL da
sala**. Quando cai, a sala deixa de contar como "transmitindo" (`broadcasting =
false`, `lastActive` reiniciado) e o relógio do TTL vira exatamente o prazo para
ele voltar e **retomar o papel**. Se não voltar, o reaper fecha a sala e solta os
espectadores — é isto que enterra a **sala zumbi** (sala de pé que ninguém pode
transmitir). Na volta, a página reenvia `broadcast-start` e a sala conta como
viva de novo.

**"Sair" é diferente de cair.** `leave-room` de quem transmite **fecha a sala na
hora** (`room-closed` com razão nova `broadcaster-left` → "Quem transmite saiu da
sala."): a sala só existe para a transmissão dele. Sair também revoga o token. De
quem assiste, `leave-room` segue sendo só uma saída.

**Frontend (`public/app.js`).**
- **Reconexão com espera crescente:** 1 s, 2 s, 4 s… até 20 s, com ruído de ±20%
  para várias páginas não voltarem em bloco depois da mesma queda. O `close`
  antigo derrubava a sala (`leaveLocal`) — agora só muda o status e agenda a
  volta. O handler de `error` saiu (o `close` sempre vem em seguida, e o retry
  mora lá). `online` e a aba voltando ao foco (`visibilitychange`) tentam na
  hora, sem esperar o timer — celular e notebook congelam aba de fundo, e aí o
  socket já está morto quando você olha de novo.
- **Sessão:** `state.resume` na memória (funciona mesmo com storage bloqueado) +
  espelho no `sessionStorage` (`concorde.session`), que atravessa **F5** e morre
  com a aba. Na volta do socket, manda `resume-session`.
- **Recuperação da mídia (`recoverMedia`, só depois de `resumed`):** na maioria
  das vezes não há nada a fazer — as conexões P2P atravessaram a queda intactas.
  É para quando a mesma queda levou a mídia junto: quem transmite reoferece a
  quem não alcança mais (`isLiveConnection`), quem assiste pede oferta nova pelo
  payload novo `need-offer` (relay comum, zero backend). Também fecha conexões
  com quem saiu enquanto estávamos fora — a lista de `peers` do `resumed` é a
  verdade, não o que a página lembrava. Com isso **F5 também volta pra sala**, e
  com vídeo.
- **Status com três estados:** "conectado" / "reconectando…" (âmbar, `--wait`,
  quando ainda temos assento pra voltar) / "desconectado". O vídeo continua na
  tela o tempo todo.
- O toast de "Falha na conexão com quem transmite" agora fica quieto se a
  sinalização também caiu: é a mesma queda, já estamos voltando e a retomada pede
  oferta nova. `rtc-report` continua saindo.
- `#CODIGO` só sai da barra no **"Sair"** (e quando a sala acaba de fato). Se a
  retomada falhar — ausente tempo demais —, o código **fica** na barra, o campo é
  preenchido e o foco vai pra senha: a sala provavelmente ainda está lá.
- `dropPeerConnection` (fecha sem mexer no `<video>`, pra imagem não piscar) sai
  de dentro do `closePeer` e passa a ser usado também no `offerTo`, que antes
  vazava a conexão antiga ao reofertar.
- Botão "Transmitir" fica desabilitado sem servidor (começar é conversa com todo
  mundo) — mas **"Parar" sempre funciona**, que é local. Formulários do lobby
  avisam em vez de engolir o clique (`requireConnection`).

**Observabilidade.** Contadores novos em `/admin/metrics`, no heartbeat `[stats]`
e como tiles no painel: "Quedas de socket", "Sessões retomadas", "Sessões
perdidas", "Retomadas recusadas" — retomadas muito abaixo das quedas significa
página que não está conseguindo voltar. `/admin/rooms` ganhou `away`, e a coluna
de participantes mostra "2 (1 ausente)": sala cheia de gente ausente é sala que
ninguém está vendo. Logs: `[room] X: viewer Y away (30s to come back)`,
`broadcaster Y away (room idle from now)`, `Y resumed as viewer`, `Y never came
back, seat released`.

**Verificado.** `go build`/`vet`/`test ./...` limpos, gofmt conferido (sem CR).
Testes novos (`resume_test.go`, 10): retomada mantém id/sala/papel e a próxima
mensagem que o outro lado recebe é o sinal de quem voltou (prova de que não saiu
`peer-left`/`peer-joined`); token rotaciona e o gasto não serve mais; token
desconhecido → `resume-failed` + métrica; tomada de socket ainda aberto; assento
abandonado expira e aí sai o `peer-left`; transmissor que cai mantém a sala e
volta como `broadcaster`; **sala com transmissor ausente é fechada pelo reaper**
(a sala zumbi); "Sair" do transmissor fecha a sala e mata o token; "Sair" de quem
assiste não fecha; carência 0 = comportamento antigo, sem token. `node --check`
nos dois JS.

**Teste ponta a ponta em Chrome real (headless, DevTools Protocol), 22
checagens, todas passando.** Servidor de teste na porta 3999 (nunca a 3000) e
Chrome com `--mute-audio`, perfil temporário, captura trocada por canvas
sintético 1080p + trilha de áudio que não vai a lugar nenhum. Confirmado: o
vídeo **continua correndo** enquanto o socket do transmissor está morto (8 → 33
quadros durante a queda) e quem assiste não recebe `peer-left` nem
`broadcast-stopped`; status vira "reconectando…" e volta pra "conectado" com o
mesmo id e sala; token rotacionado e guardado; queda do socket **e** do P2P
juntos → vídeo volta sozinho pelo `need-offer`; **F5 no espectador volta pra
sala e pro vídeo**; "Sair" do transmissor avisa quem assiste e limpa link e
token; assento abandonado expira (carência de 3 s); métricas e `away` no painel
batendo.

> **Como testar aqui de novo** (o harness fica fora do repo, no scratchpad):
> `app.js` é **ES module**, então `state` não existe no escopo global e
> `Runtime.evaluate` não enxerga nada. O harness embrulha `WebSocket` e
> `RTCPeerConnection` num script injetado **antes** do app
> (`Page.addScriptToEvaluateOnNewDocument`) e lê dali as mensagens recebidas, o
> socket pra cortar e as conexões pra medir — sem um único gancho de teste no
> código que vai pro ar. Outra pegadinha: aba headless conta como **oculta**, e
> página oculta tem timer estrangulado em ~1 Hz — a captura sintética cai pra ~1
> fps e uma transmissão saudável parece congelada. Precisa de
> `--disable-background-timer-throttling`, `--disable-renderer-backgrounding`,
> `--disable-backgrounding-occluded-windows` e
> `Emulation.setFocusEmulationEnabled`.

**Não verificado aqui:** `-race` não roda nesta máquina (só há gcc 32-bit; o
detector precisa de cgo 64-bit) — a análise das corridas foi feita na mão: tudo
que a retomada escreve (`id`, `room`, `role`, `token`, `expiry`) é escrito sob
`Hub.mu`, e `away` é atômico justamente porque o `enqueue` o lê fora do lock.
Falta também o comportamento atrás do túnel real, com NAT de verdade e troca de
rede no celular — isso é a tua validação.

---

## Sessão 17 — Qualidade: estatísticas, bitrate, áudio estéreo e medição de TURN (2026-09-25)

Branch: `main`. Passo 2 do `Plano.md`. Frontend (JS/HTML/CSS) + um pedaço de
backend (Go) pra medição. **Primeira sessão testada em navegador real** (Chrome
headless dirigido por script — ver "Verificado").

**Ajustes de qualidade (`public/app.js`, lado de quem transmite).**
- **Vídeo:** `contentHint = "motion"` na track (captura de tela vem como
  "detail", que preserva resolução e derruba fps; anime é vídeo em movimento).
  Teto de bitrate **8 Mb/s por espectador** (`VIDEO_MAX_BITRATE`) via
  `addTransceiver(track, {direction: "sendonly", streams, sendEncodings})` — o
  `addTrack` saiu; o teto vale desde o primeiro quadro, sem `setParameters`
  depois. O controle de congestionamento continua baixando abaixo do teto se o
  link não aguenta.
- **Partida do bitrate:** o Chrome começa a estimativa de banda em ~300 kb/s;
  `x-google-start-bitrate=3000` (kb/s, `VIDEO_START_BITRATE`) no fmtp de cada
  codec de vídeo faz começar em 3 Mb/s.
- **Áudio:** Opus **estéreo a 128 kb/s** (`AUDIO_BITRATE`; o padrão é ~32 kb/s
  mono, qualidade de chamada). `contentHint = "music"` e `channelCount: {ideal:
  2}` na captura.
- **Como:** o encoder de quem envia segue as preferências de quem recebe no
  fmtp da *answer*, e não existe API pra isso — então `tuneAnswer` edita a
  answer do espectador **no transmissor, antes do `setRemoteDescription`**
  (`stereo=1;maxaveragebitrate=128000` no Opus; `x-google-start-bitrate` nos
  codecs de vídeo; rtx/RED/FEC intactos). `setFmtp`/`mergeFmtp` mesclam
  parâmetros e criam a linha fmtp se não existir (o VP8 do Chrome não tem).

**Painel de estatísticas (`getStats`).** Botão novo (ícone de barras,
`#stats-btn`, `aria-pressed`) nos controles do vídeo liga um overlay no canto
superior esquerdo (`#stats-overlay`, monoespaçado, `pointer-events: none`),
atualizado a cada 1 s e **só consultando enquanto visível**. A escolha fica
lembrada no navegador (`localStorage`, com try/catch).
- **Quem assiste:** resolução · fps / bitrate de vídeo (codec) · bitrate de
  áudio / caminho · RTT · perda.
- **Quem transmite:** uma entrada por espectador (cada um tem seu encoder) com o
  que está saindo e o **gargalo** do encoder (`qualityLimitationReason`:
  nenhum / banda / CPU) — diz o que resolver quando a qualidade cai. Perda vem
  do RTCP do espectador (`remote-inbound-rtp`). Os controles do transmissor
  agora aparecem só com esse botão (volume e tela cheia escondidos).
- Bitrate e perda são taxas do último intervalo: `readStats` guarda o relatório
  anterior na própria PC (`pc.lastStats`). Caminho vem do par de candidatos
  selecionado (`transport.selectedCandidatePairId`; fallback `selected` do
  Firefox): "P2P direto" (host↔host: mesma rede ou IPv6 público), "P2P via NAT
  (STUN)" (srflx/prflx) ou "via TURN (relay)".

**Medição de TURN (backend + frontend).** Mensagem nova cliente→servidor
`rtc-report {outcome, localType, remoteType, protocol}`, enviada **só por quem
assiste** (uma conexão cada, nada contado duas vezes): `connected` (na primeira
vez que conecta, com o caminho), `failed` (falhou **sem nunca conectar** = esse
par precisaria de TURN) ou `dropped` (conectou e caiu no meio).
- `internal/signaling/rtc.go` (novo): `Hub.rtcReport` ignora em silêncio
  (sem erro de volta) relatório do transmissor, de fora de sala ou com outcome
  desconhecido; tipos de candidato/protocolo passam por whitelist antes do log.
  Log `[rtc] room X: viewer Y connected (srflx→host udp)` /
  `failed to connect (no path without TURN)` / `dropped mid-stream`.
- Contadores `p2pConnected`/`p2pFailed`/`p2pDropped` no `/admin/metrics`, no
  heartbeat `[stats]` (que também ganhou o `lockouts` que faltava) e tiles no
  painel: "P2P conectadas", "P2P sem conexão (TURN)" e "P2P caídas" (esses dois
  com alarme).

**Verificado.** `go build`/`vet`/`test ./...` limpos (gofmt checado sem CR).
Testes novos (`rtc_test.go`): os três outcomes contados; relatório do
transmissor, de fora de sala e outcome inválido ignorados sem erro; whitelist.
`node --check` nos dois JS; teste isolado das funções puras (`tuneAnswer` com
e sem linha fmtp, rtx/RED intactos, idempotência, formatação do overlay).

**Teste ponta a ponta em Chrome real (headless, via DevTools Protocol).** Script
fora do repo (scratchpad): Chrome 153 headless com perfil temporário e
`--mute-audio`, servidor de teste na porta 3999, abas transmissor + espectador(es).
A captura foi trocada, só nessas abas, por uma fonte sintética (canvas 1920×1080
a 30 fps imitando anime — cenário detalhado em panorâmica + formas chapadas — e
ruído estéreo que não toca em lugar nenhum), porque o headless ignora a escolha
de aba e captura a tela. Resultados (loopback, sem limite de banda):
- **A/B do teto:** sem teto (padrão do Chrome) o encoder trava em
  `targetBitrate` **2 Mb/s → 960×540**. Com o teto: **1920×1080 · 30 fps ·
  ~8 Mb/s, gargalo "nenhum"**.
- **Partida:** sem `x-google-start-bitrate`, 360p nos primeiros ~30 s e 1080p
  só aos ~45 s. Com 3 Mb/s: 720p a ~6–8 Mb/s em 5 s, **1080p aos ~20 s**.
- **Áudio:** answer aplicada com `stereo=1;maxaveragebitrate=128000`, encoder
  com `targetBitrate: 128000` e 2 canais; espectador recebe ~128–130 kb/s.
- **Sala cheia (2 espectadores):** o segundo entra no meio da transmissão e
  recebe vídeo; overlay lista os dois. Nesta máquina apareceu **"gargalo: CPU"**
  (dois encoders VP8 1080p em software + o próprio teste gerando o canvas e
  decodificando os dois espectadores) — em uso real o transmissor só codifica,
  mas é o sinal a observar.
- **Falha de ICE simulada** (candidatos remotos trocados por um IP
  inalcançável, 192.0.2.1): "connecting" → "failed" em ~30 s, toast no
  espectador, `rtc-report failed`, `p2pFailed` = 1 e o log `no path without
  TURN`. (Sem *nenhum* candidato remoto o Chrome fica em "checking" sem nunca
  falhar — cenário irreal, NAT de verdade sempre troca candidatos.)
- Parada limpa ("A transmissão foi encerrada.", overlay some) e relatório
  `connected (host→host udp)` conferidos em todas as rodadas.

> **Lição (Windows):** headless **não é mudo**. A primeira rodada, sem
> `--mute-audio`, capturou a tela *com áudio do sistema*, e a aba espectadora
> tocava o que recebia — microfonia digital nos alto-falantes. Sempre
> `--mute-audio`; e só matar processos com o caminho do perfil temporário na
> linha de comando (a porta 3000 é o servidor de produção nesta máquina).

**Não verificado aqui:** captura de **aba** real (o headless captura a tela), e
qualquer coisa com banda limitada de verdade ou NAT real — isso é a tua
validação em produção (episódio inteiro + os números de TURN no painel).

---

## Sessão 16 — Limite de tentativas de senha (2026-09-25)

Branch: `main`. Backend (Go) + mensagens no frontend. A sessão também definiu a
direção do projeto e a ordem dos próximos passos (registradas no `Plano.md`).

**Limite (`internal/signaling/login.go` novo).** `loginLimiter`: depois de
`LOGIN_MAX_FAILURES` (default 3) senhas erradas seguidas, o visitante fica
bloqueado por `LOGIN_LOCKOUT_MINUTES` (default 15) — nem a senha certa entra
nesse meio-tempo. Senha certa zera a sequência; erros mais espaçados que o
bloqueio não se somam. Admin e sala contam separado (chaves `admin|IP` e
`room|IP`), então um amigo errando a senha da sala não tranca o admin. O mapa é
podado a cada minuto (não cresce sem limite). `≤0` desliga.
- **Onde vale:** como admin, o `create-room` (WS) e os endpoints HTTP do painel
  (`authorizeAdmin` substitui o `checkAdmin`: 429 + `Retry-After` durante o
  bloqueio, 401 com credencial errada; request sem credencial não conta). Como
  sala, o `join-room` com senha errada (sala inexistente não conta).
- **IP do visitante (`clientIP`).** Atrás do túnel, todo request chega do
  `cloudflared` local (loopback), então o IP real vem do `CF-Connecting-IP` —
  confiado só quando a conexão vem de loopback (um cliente direto na rede não
  consegue forjar). Sem isso, 3 erros de qualquer um trancariam todo mundo.
- A senha de admin no WS agora também é comparada em tempo constante
  (`adminCredentialsOK`, compartilhada com o HTTP).
- **Observabilidade:** log `[auth] ... locked out`, contador `loginLockouts` no
  `/admin/metrics` e tile "Bloqueios de login" (com alarme) no painel.
- **Frontend:** código `too-many-attempts` → "Muitas senhas erradas. Espere
  alguns minutos e tente de novo." (no formulário certo, via `pendingForm`); o
  login do painel trata o 429.
- **Config:** `LOGIN_MAX_FAILURES` e `LOGIN_LOCKOUT_MINUTES` em `config.go` e
  `.env.example`.

**Verificado:** `go build`/`vet`/`test ./...` limpos. Testes novos
(`login_test.go`): o limitador (trava no 3º erro, destrava após o bloqueio,
sucesso zera, erros espaçados não somam, `0` desliga), `clientIP` (confia no
header só via loopback) e o bloqueio de ponta a ponta no WS (admin e sala, com
contagens separadas e a métrica) e no HTTP (401×3 → 429 com `Retry-After`; sem
credencial não conta). `node --check` nos dois JS. Smoke test (porta 3999): 401,
401, 401 → 429 com `Retry-After: 900`; outro visitante (via
`CF-Connecting-IP`) → 200; `loginLockouts` = 1.

**Nota (Windows/gofmt):** o working tree usa CRLF (`core.autocrlf=true`) e o
`gofmt -l` acusa todo arquivo por isso. Para checar a formatação, remover o CR
antes (`tr -d '\r' < f | gofmt`); não rodar `go fmt ./...` direto, que reescreve
tudo em LF.

**Não verificado aqui:** o bloqueio atrás do túnel real (depende do
`CF-Connecting-IP`, que a Cloudflare envia por padrão) e as mensagens no
navegador.

---

## Sessão 15 — URL individual de sala + rejoin por link (Fase B) (2026-09-25)

Branch: `main`. Só frontend (JS + HTML); backend intacto. Primeira parte da
"Persistência / URL de sala" do `Plano.md` — a **versão leve**, decidida junto:
link compartilhável por sala enquanto ela existe, sem storage novo, sem depender
do Pi nem de deploy. A versão durável (SQLite, sobrevive a restart) fica pra
depois, junto do Passo 7 (durabilidade no Pi).

**Escopo e decisões (conversadas antes):**
- **Persistência:** nenhuma nova — a sala segue em memória e some no TTL/reboot.
  O link só serve pra reentrar **enquanto a sala está viva**; se já morreu, o
  fluxo normal avisa "sala não encontrada".
- **Rota:** fragmento `/#CODIGO` (client-side), não `/r/CODIGO`. Zero backend, e
  o fragmento **nunca é enviado ao servidor** — o código não vaza em log/analytics.
- **Segurança:** a senha **nunca** vai na URL; continua sendo digitada sempre.

**Frontend (`public/app.js`), nova seção "room URL (hash)":**
- `codeFromHash()` lê `location.hash`, faz `decodeURIComponent`, trim, uppercase e
  tira tudo fora de `[A-Z0-9]` (o servidor valida de verdade depois).
- `setRoomHash(code)`/`clearRoomHash()` refletem a sala na barra via
  `history.replaceState` — sem entrada nova de histórico e, de propósito, **sem
  disparar `hashchange`** (diferente de atribuir `location.hash`), então nossas
  próprias mudanças não voltam pra `applyHashToJoinForm` num laço.
- `applyHashToJoinForm()` (guarda `state.roomId === null`): fora de uma sala, um
  link com código pré-preenche `#join-room-id` e foca a senha — o amigo só digita
  a senha e dá Enter. Chamado no load e no `hashchange` (link colado na barra).
- Ciclo: `enterRoom` chama `setRoomHash(roomId)` (barra espelha a sala);
  `leaveLocal` chama `clearRoomHash()` (sair/cair/fechar limpa a barra).
- `roomLink()` = `origin + "/#" + roomId`. O botão "copiar" da sala passou a
  **copiar o link** (antes copiava só o código): texto/rótulo viraram "copiar
  link" (`index.html` + confirmação inline "copiado!" + toast de erro).

**Verificado:** `node --check public/app.js` ok; `go build`/`vet`/`test ./...`
limpos (backend intacto). Smoke test (porta 3999): `/healthz` 200, index com o
botão "copiar link", `app.js` com `applyHashToJoinForm`/`setRoomHash`/
`clearRoomHash`/`roomLink`/`codeFromHash`.

**Não verificado aqui (precisa de você, navegador real):** abrir um link
`.../#CODIGO` e ver o código pré-preenchido + foco na senha; o botão copiar
entregando o link certo; a barra espelhando/limpando ao entrar/sair. Mesma
limitação de sempre (sem navegador aqui).

**Nota (discrepância corrigida no `Plano.md`):** o keepalive da Sessão 14 **já
está no `main`** (commit `1853898`), não mais num branch isolado — ou seja, a
observabilidade (Sessão 13) e o keepalive (14) sobem juntos no próximo deploy; a
ideia de baselinar um antes do outro não aconteceu.

---

## Sessão 14 — Keepalive do WebSocket (branch) (2026-09-22)

Branch: `session-14-ws-keepalive` (fora do `main` de propósito — pra você subir a
observabilidade da Sessão 13 primeiro e ter o baseline dos logs de duração antes
de mudar comportamento). Ataca a hipótese do sintoma "desconexões no meio": depois
que o P2P sobe, o socket de sinalização fica quieto e o túnel Cloudflare fecha por
ociosidade (~100s), o que pro usuário parece uma queda no meio da sessão.

**`pingLoop` (`client.go`).** Uma goroutine por cliente manda um ping WebSocket a
cada `WS_PING_SECONDS` (default 30; ≤0 desliga). O ping é tráfego que zera o
relógio de ociosidade do túnel; esperar o pong (timeout de 10s, `pingTimeout`)
também detecta peer morto na hora — ping que falha chama `CloseNow`, o `readLoop`
destrava e a limpeza normal roda. Os clientes reais têm `readLoop` sempre lendo,
então a lib do peer responde os pings com pong sozinha. Protegida por
`recoverPanic` como as outras goroutines de fundo.

**Wiring.** `ServeWS` (`hub.go`) sobe o `pingLoop` junto do `writeLoop` quando
`WS_PING_SECONDS > 0`. `config.go` + `.env.example` documentam a flag.

**Verificado:** `gofmt`/`build`/`vet`/`test ./...` limpos. Teste novo
(`keepalive_test.go`, `TestKeepAliveSurvivesIdle`, ~2,2s): com ping de 1s, uma
conexão fica ociosa por ~2 intervalos e continua viva e funcional (cria sala e
recebe `room-created`) — trava a regressão de o pinger derrubar socket saudável. O
teste **não** prova que o timeout do túnel foi vencido (isso é `httptest`, sem
timeout de ociosidade) — isso se valida em produção pelos logs `disconnected after
<dur>`: sem keepalive, mortes agrupadas perto de ~100s; com keepalive, esse padrão
some.

**Como validar depois do deploy:** subir o `main` (Sessão 13) primeiro, olhar os
logs de duração por uns dias. Se aparecer o padrão ~100s, mergear este branch e
confirmar que some.

---

## Sessão 13 — Logs + métricas (observabilidade) (2026-09-22)

Branch: `main`. Só backend (Go). Ataca o feedback pendente desde a Sessão 11
("servidor parece instável; adicionar logs e métricas pra diagnosticar") — e, de
quebra, tira do caminho uma causa real de instabilidade (panic em goroutine de
fundo derrubava o processo inteiro).

**Métricas (`internal/signaling/metrics.go` novo).** `Metrics` com contadores
atômicos (`sync/atomic`, lock-free) do tempo de vida do processo:
`connectionsOpened`/`connectionsClosed` (a diferença = conexões ativas; se sobe
sem parar, socket vazando), `roomsCreated`/`roomsClosed`, `signalsRelayed`,
`errorsSent`, `droppedMessages` (buffer de envio estourado — sinal direto de peer
lento/travado), `acceptFailures` e `panicsRecovered`. `MetricsSnapshot` é a visão
serializável: soma os atômicos + uma leitura sob `Hub.mu` das salas/participantes
ativos + uptime. O `Hub` ganhou `startedAt` e um `Metrics` embutido (não copiável;
tudo acessa via `*Hub`).

**Instrumentação.** Contagem espalhada nos pontos de ciclo de vida: `ServeWS`
(open/close via `defer`, e `acceptFailures` no erro de accept), `createRoom`
(`roomsCreated`), `removeLocked`+`closeRoomLocked` (`roomsClosed`, os dois
caminhos de remoção de sala), `relay` (`signalsRelayed`). `errorsSent` e
`droppedMessages` são contados num único ponto — `Client.enqueue` (choke point de
tudo que vai pro cliente), com guarda `c.hub != nil` porque alguns testes montam
`Client` sem hub.

**Endpoint `GET /admin/metrics`** (`admin.go`, `ServeAdminMetrics`). HTTP Basic de
admin como os outros endpoints (os números — e o fato de o servidor estar no ar —
não ficam públicos); responde o `MetricsSnapshot` em JSON.

**Heartbeat no log (`StartStatsLogger`).** Ticker igual ao do reaper: a cada
`STATS_INTERVAL_MINUTES` (default 15; ≤0 desliga) loga uma linha `[stats]` com o
snapshot. Serve pro pós-morte: se o servidor cai/reinicia, dá pra varrer o log e
ver se conexões/salas vinham subindo ou se um panic disparou antes do silêncio,
sem precisar ter batido no `/admin/metrics` na hora certa.

**Recuperação de panic (robustez, não só observabilidade).** `Hub.recoverPanic`
(`runtime/debug.Stack`) agora protege as goroutines que o `net/http` **não**
cobre: `writeLoop` (uma por cliente), o reaper e o stats logger. Antes, um panic
em qualquer uma delas derrubava o processo inteiro — exatamente o tipo de coisa
que explica "instabilidade". Agora vira log `[panic]` + incremento de
`panicsRecovered`, e só aquela goroutine morre.

**Config/wiring.** `STATS_INTERVAL_MINUTES` (default 15) em `config.go` e
`.env.example`; `main.go` chama `hub.StartStatsLogger` e registra
`/admin/metrics`.

**Verificado:** `gofmt`/`go build`/`vet`/`test ./...` limpos. Testes novos
(`metrics_test.go`): fluxo real create→join→relay→erro pela socket confere os
contadores (`roomsCreated`, `connectionsOpened`/ativas, `activeRooms`/
`participants`, `signalsRelayed`, `errorsSent`); `/admin/metrics` exige auth (401)
e devolve o snapshot com auth; `recoverPanic` engole e conta um panic. Smoke test
(porta 3999): `/healthz` 200, `/admin/metrics` 401 sem auth e JSON completo com
auth, e o log de startup com a linha `[stats] heartbeat disabled` quando o
intervalo é 0.

**Não verificado aqui:** o heartbeat disparando de fato (intervalo mínimo é 1 min;
a lógica espelha o reaper, que é testado). Nada disso precisa de navegador.

**Continuação (mesmo dia) — painel visual + log de duração.** A pedido teu, as
métricas ganharam uma cara e o backend um log que ataca o sintoma "desconexões no
meio":
- **Painel visual no `/admin.html`.** Um card "Métricas do servidor" acima da
  lista de salas, mostrando uptime + tiles com cada contador, atualizando junto
  com o auto-refresh de 5s que já existia. `admin.js`: `refresh()` agora dispara
  `refreshRooms()` e `refreshMetrics()` em paralelo (`Promise.all`; um falhar não
  apaga o outro, cada um tem seu próprio slot de erro). `renderMetrics` monta as
  tiles de `STAT_FIELDS`; os contadores que **deveriam** ficar em zero num
  servidor saudável (`droppedMessages`, `acceptFailures`, `panicsRecovered`)
  ganham a classe `.stat--alarm` (borda + número vermelhos) quando passam de 0.
  `formatUptime` formata d/h/m/s. CSS novo: `.stat-grid` (grid responsivo),
  `.stat`, `.stat__label/__value`, `.stat--alarm`, `.admin__uptime`.
- **Log de duração da conexão.** `ServeWS` agora loga
  `[ws] client X disconnected after <dur>`. É o dedo-duro pro sintoma relatado
  (desconexões no meio da sessão): se muitas conexões morrem sempre por volta do
  mesmo tempo (ex.: ~100s), aponta pra **timeout de conexão ociosa** no túnel
  Cloudflare (o WS fica quieto depois que o P2P sobe) — e aí o próximo passo seria
  um keepalive/ping no WebSocket. Só medição por ora; nenhuma mudança de
  comportamento.

**Verificado (continuação):** `gofmt`/`build`/`vet`/`test ./...` limpos;
`node --check public/admin.js` ok; smoke test (porta 3999, servidor fresco):
`/admin.html` com `#metrics`/`#stat-grid`/`#uptime`, `styles.css` com
`.stat-grid`/`.stat--alarm`, `admin.js` com `renderMetrics`/`refreshMetrics`/
`formatUptime`, e `/admin/metrics` devolvendo o snapshot (uptime baixo,
confirmando processo novo). **Não verificado aqui:** o visual do painel em si
(sem navegador).

**Frentes em aberto (no `Plano.md`):** ajustes de UI da Fase B aguardam tua
validação em navegador; persistência/URL de sala precisa de conversa antes;
durabilidade (Passo 7) espera o Raspberry Pi. **Hipótese anotada:** keepalive no
WebSocket contra desconexão por ociosidade no túnel — investigar com o log de
duração depois do deploy.

---

## Sessão 12 — Correções de UI da Fase B (2026-09-22)

Branch: `main`. Só frontend (HTML/CSS/JS); backend intacto. Fecha as correções de
UI pendentes da Fase B mais uma nova pedida na volta (aviso de senha errada / sala
inexistente).

**Aviso de senha errada / sala inexistente (novo).** O mapeamento de erro já
existia (`errorText` → "Senha da sala incorreta." / "Sala não encontrada."), mas
caía num textinho discreto **abaixo dos dois formulários**, fácil de não ver. Agora
cada formulário tem seu próprio slot de erro (`#create-error`/`#join-error`) num
**box tinto** (`.form-error`, borda + fundo `--danger`) logo abaixo do botão. O
`app.js` roteia o erro pro formulário certo via `state.pendingForm` (marcado no
submit de cada form) — resolve a ambiguidade do código `bad-room-password`, que o
servidor usa tanto no create (senha de sala vazia) quanto no join (senha errada).
O `#lobby-error` compartilhado saiu.

**Card da sala colado no player.** O card do room-view ficava ~50px abaixo do
player porque, para o broadcaster, o botão "Transmitir" ocupa uma linha entre os
dois. Regra (`styles.css`, ≥641px): quando o botão está visível,
`.stage:has(.stage__actions > button:not([hidden])) + .panel #room-view` recebe
`position: relative; top: -50px` (12px do gap da `.stage` + ~38px do botão),
puxando o card pra ~16px abaixo do player, igual aos forms do lobby. O card é
left-aligned e cap de 420px, o botão é right-aligned, então não colidem.

> **Correção no mesmo dia (regressão):** a primeira versão puxava o `.panel`
> inteiro com `margin-top: -50px`. Como o painel ocupa a **largura toda**, ele
> ficava (invisível) por cima do botão "Transmitir" e **engolia os cliques do
> mouse** — só o Tab+Enter funcionava (foco por teclado não passa por
> hit-testing). Trocado por `position: relative` **só no `#room-view`** (estreito,
> ≤420px, à esquerda), deixando o botão (à direita) livre.

**Controles auto-ocultam em tela cheia.** Antes ficavam sempre visíveis em
fullscreen. Agora `onFullscreenChange` (substitui o antigo listener que só
sincronizava o botão) anexa um `mousemove` no `#video-window` só enquanto em
fullscreen; após 2,5s sem movimento adiciona `.controls-hidden` (fade dos
controles + `cursor: none` via CSS), e o próximo movimento remove. A regra
`.video-window:fullscreen.controls-hidden` tem specificity maior que o `:hover`,
então o ponteiro parado sobre a janela ainda deixa ocultar.

**Botão de copiar código.** Botão "copiar" ao lado do código da sala
(`#copy-code`, `.copy-btn`) → `navigator.clipboard.writeText(state.roomId)`, com
confirmação inline ("copiado!" em verde por 1,5s) e toast de erro no fallback.

**Barra de scroll on-theme.** `scrollbar-width/color` (Firefox) + regras
`::-webkit-scrollbar*` (Chromium): trilho transparente, thumb `--border`
(→`--off` no hover), 10px.

**Verificado:** `node --check` no `app.js` ok; `go build`/`vet`/`test ./...`
limpos (backend intacto). Smoke test (porta 3999): index com `#copy-code`,
`#create-error`, `#join-error`; CSS com `.form-error`, `.copy-btn`,
`.controls-hidden`, `::-webkit-scrollbar`; `app.js` com `copyCodeBtn`,
`onFullscreenChange`, `revealControls`.

**Não verificado aqui (precisa de você, navegador real):** o visual/comportamento
de tudo — em especial (1) o card colado no player (o `-50px` é calculado; se
sobrar/faltar um triz me diz que ajusto, ou movo o botão pra dentro do card) e (2)
o auto-ocultar em tela cheia (timing de 2,5s). Mesma limitação de sempre (sem
navegador aqui).

**Registrado no `Plano.md` (não commitado):** ideia de **URL individual de sala +
rejoin por URL/código+senha e persistência** — só anotada, com as decisões a tomar
antes de implementar (o que persiste, onde guardar, rota, senha nunca na URL);
encaixa junto da durabilidade na Fase C.

---

## Sessão 11 — Deploy: `concorde.example.com` no ar (Fase C) (2026-09-21)

Branch: `main`. Primeira parte da Fase C: o serviço está **público e com HTTPS**
em `concorde.example.com`, hospedado de casa via **Cloudflare Tunnel**.

**Decisões (Fase C):**
- **Host:** casa + **Cloudflare Tunnel** (grátis; o servidor Go roda como está e o
  `cloudflared` expõe pela borda da Cloudflare, sem abrir porta no roteador). É um
  estágio até o host dedicado (Raspberry Pi) — migrar depois é trivial (mesmo
  binário; roda o mesmo `cloudflared` no destino).
- **Endereço:** `concorde.example.com` (subdomínio na zona já existente). O
  `share.example.com` chegou a ser criado e depois **descartado** (fora do
  ingress); o CNAME órfão pode ser apagado no painel quando quiser.
- **TURN:** segue **sem** (só STUN), como planejado.

**Código (blindagem pra ficar exposto):**
- `ALLOWED_ORIGINS` configurável (`config.go` + `hub.go`): a checagem de origem
  do WebSocket deixa de ser só same-origin. Vazio = same-origin (default seguro
  do dev); em produção, `ALLOWED_ORIGINS=concorde.example.com` faz a origem do
  navegador ser aceita mesmo se o proxy/túnel não preservar o `Host` — sem isso o
  `websocket.Accept` rejeitaria com 403 e derrubaria toda a sinalização.
- `main.go`: aviso no startup se `ADMIN_PASSWORD` ainda for o default `change-me`.
- `.env.example` documenta `ALLOWED_ORIGINS`; `.gitignore` ignora `rascunhos.md`.
- Teste novo `TestWebSocketOriginCheck`: origem estranha é recusada por padrão e
  a origem da allow-list conecta.

**Infra (fora do repo — na máquina/na conta Cloudflare):**
- `cloudflared` instalado via winget (v2026.9.1), em
  `C:\Program Files (x86)\cloudflared\`.
- Túnel **locally-managed** `concorde` criado (`cloudflared tunnel create`), com
  credenciais e `config.yml` em `%USERPROFILE%\.cloudflared\`. Ingress:
  `concorde.example.com` → `http://localhost:3000`, resto → 404.
- DNS: CNAME criado via `cloudflared tunnel route dns concorde
  concorde.example.com`; SSL automático da Cloudflare.

**Verificado:** `go build`/`vet`/`test ./...` limpos. Pela borda pública:
`/healthz` 200, index 200, e **WebSocket** `wss://concorde.example.com/ws` com a
origem de produção recebendo o `hello` — HTTPS + WS + allow-list de origem todos
funcionando pelo túnel. **Teste ponta a ponta no navegador passou (confirmado por
você):** vídeo + áudio + P2P entre redes diferentes.

**Ainda não feito (Passo 7 — durabilidade):** hoje o servidor (`go run`) e o
`cloudflared` (`tunnel run`) sobem **na mão**; reboot/fim de sessão derruba os
dois e o site sai do ar até subir de novo. Instalar como serviços (subir no boot)
fica para o **host dedicado (Raspberry Pi)** — no Linux vira `systemd`. Decisões
de hardware conversadas: Pi 4 (2–4 GB, RAM soldada), na tomada, roda vários
serviços; Arduino é inviável (microcontrolador, sem SO).

**Feedback da Fase B registrado (no `Plano.md`, não commitado):** posição do card
de sala no room-view; controles (volume/tela cheia) que nunca somem em fullscreen;
botão de copiar código da sala; e **logs + métricas** pra investigar a sensação de
instabilidade (parte dela é o restart manual, que a durabilidade resolve).

**Próximo:** à escolha na volta — (a) comprar o Pi e fazer a durabilidade lá
(Passo 7, `systemd`), (b) logs + métricas, ou (c) as correções de UI acima.

---

## Sessão 10 — Parada de transmissão limpa para os viewers (Fase B) (2026-09-19)

Branch: `main`. Backend (Go) + frontend (JS).

Fecha o item que a Sessão 9 deixou em aberto: quando o broadcaster parava
normalmente, o viewer só percebia pela conexão ICE decaindo para `failed` alguns
segundos depois, o que disparava o toast "Falha na conexão com quem transmite" —
assustador para uma parada normal. Agora a parada é avisada e o viewer derruba a
conexão de forma limpa, sem erro.

**Servidor (`internal/signaling/`):**
- Novo `broadcastStoppedMsg` (`{type:"broadcast-stopped"}`) em `messages.go` —
  notificação server→cliente, no mesmo padrão de tempo-passado de
  `peer-left`/`room-closed`.
- `setBroadcasting` (`hub.go`), ao receber `broadcast-stop`, agora **relaya
  `broadcast-stopped` aos demais participantes** da sala (todos menos o próprio
  broadcaster). Já está sob o lock e com a sala em mãos, então é só um range
  sobre `room.clients`. `broadcast-start` não relaya nada — a própria offer já
  chega a cada viewer.

**Frontend (`public/app.js`):**
- Novo `case "broadcast-stopped"` → `handleBroadcastStopped`: para os viewers,
  chama `stopMedia()` (fecha as PCs — uma conexão que **nós** fechamos reporta
  `closed`, não `failed`, então o toast de falha não dispara) e mostra um toast
  **informativo** "A transmissão foi encerrada." no lugar do erro. O broadcaster
  continua na sala, então um recomeço só traz uma offer nova (compõe com a
  correção da Sessão 9, que já troca a PC velha pela nova ao receber outra offer).

**Verificado:** `go build`/`vet`/`test ./...` limpos; teste novo
`TestBroadcastStopNotifiesViewers` (o viewer recebe `broadcast-stopped` no stop;
`broadcast-start` não relaya nada; o broadcaster não é notificado do próprio stop
— tudo por ordenação de mensagens, sem timeouts). `node --check` nos dois JS ok.
Smoke test do servidor (porta alternativa): `/healthz` ok, index 200, `app.js`
200 já com o `case` e o handler novos.

**Não verificado aqui (precisa de você, navegador real):** confirmar que parar a
transmissão não gera mais o toast de erro no viewer, e sim o aviso "A transmissão
foi encerrada." — mesma limitação de sempre (sem navegador aqui).

---

## Sessão 9 — Revisão e limpeza de código (2026-09-15)

Branch: `main`. Sem feedback visual novo nesta rodada — passada de qualidade no
código (frontend + backend) procurando bugs e inconsistências.

**Achado e corrigido — vazamento de `RTCPeerConnection` no viewer.** Em
`handleSignal`, ao receber um `offer`, o viewer criava uma PC nova sob a chave
`from` **sem fechar** uma PC anterior para o mesmo peer. Como o id do broadcaster
não muda, parar e recomeçar a transmissão fazia o viewer receber um segundo
`offer` e deixava a PC velha órfã (ICE/mídia nunca liberados). Agora o `offer`
fecha e remove qualquer PC existente para aquele peer antes de criar a nova.

**Achado e deixado para depois (não é limpeza, é feature).** Quando o broadcaster
para normalmente, o `stopMedia` fecha as PCs do lado dele e a PC do viewer cai em
`failed` segundos depois, disparando o toast "Falha na conexão com quem
transmite" — assustador para uma parada normal. Corrigir direito exige relayar
`broadcast-stop` aos viewers (adição ao protocolo), então fica como decisão para
uma próxima sessão.

**Resto da revisão:** backend (Go) e o restante do frontend estão limpos —
bem comentados, testados e consistentes. Sem outras mudanças. A classe CSS
`.topbar` segue em uso (header do `/admin.html`), apesar de o index não usar mais.

**Verificado:** `go build`/`vet`/`test ./...` limpos (backend intacto);
`node --check` nos dois JS ok. A correção do vazamento não tem como ser vista sem
navegador — o comportamento visível (recomeçar a transmissão) continua igual;
muda só que a PC antiga não fica mais pendurada.

---

## Sessão 8 — Status no topo (Fase B) (2026-09-15)

Branch: `main`. HTML + CSS.

O link "admin" e o indicador de conexão saíram do rodapé e foram para um
**cabeçalho acima do player, alinhados à direita**, lado a lado. A marca
"Concorde" continua centralizada, agora na mesma linha do status.

- `index.html`: o `<h1>` da marca e o antigo `<footer class="statusbar">` viraram
  um `<header class="header">` com a marca + um `<div class="statusbar">` (mesmo
  conteúdo, mesmo `#conn-status`). O rodapé de baixo deixou de existir.
- `styles.css`: `.header` é um grid de 3 colunas
  (`minmax(0,1fr) auto minmax(0,1fr)`) — a marca fica na coluna central (segue
  centralizada na página, independente da largura do status) e o status na
  direita (`justify-self: end`). As margens verticais do header preservam o
  espaçamento simétrico marca↔topo / marca↔player da Sessão 7. `.header
  .statusbar` zera o `margin-top: auto` (que só serve ao rodapé do admin).
- O `.statusbar` base e a página de admin não mudaram (admin ainda usa como
  rodapé).

**Verificado:** smoke test — index com `<header>`, status dentro dele, sem
`<footer>`; `#conn-status` preservado; CSS com as regras do header. Visual final
depende de você (navegador).

---

## Sessão 7 — Ajustes finos de espaçamento (Fase B) (2026-09-15)

Branch: `main`. Só CSS.

Segunda rodada de ajustes de layout, a partir do teu feedback:

- **Room-view alinhado à esquerda.** Tirei o `margin: 0 auto` do `#room-view`:
  quando a sala é criada, o card da sala agora fica à esquerda (como os forms do
  lobby), não mais centralizado.
- **Gap player↔forms igual ao gap entre os forms.** No lobby o botão de
  transmitir fica escondido, mas a `.stage__actions` (vazia) + o `gap` da
  `.stage` deixavam um espaço morto sob o player, tornando o vão player↔forms
  maior que o vão entre os dois forms. Agora `.stage__actions:has(> [hidden])`
  colapsa a linha quando o botão está escondido, então os dois vãos ficam iguais
  (16px) e os forms sobem para perto do player.
- **Espaço acima e abaixo de "Concorde" igual.** Um `margin-bottom: 4px` no
  título (somado ao `gap` da `.app`) iguala o vão marca↔player ao vão topo↔marca,
  descendo o player os 4px que faltavam.

**Verificado:** smoke test do servidor — `styles.css` servido com as regras
novas; index 200. Visual final depende de você (navegador).

---

## Sessão 6 — Ajustes de layout (Fase B) (2026-09-15)

Branch: `main`.

Primeira rodada de ajustes da Fase B, a partir do teu feedback na primeira
versão. Tudo frontend (layout/CSS + limpeza); backend intacto.

- **Confirmações removidas.** Os dois `<dialog>` da Sessão 5 saíram: clicar
  "Transmitir" vai direto ao seletor de janela do navegador (que já é a chance
  de desistir) e "Parar transmissão" para na hora. Removidos o HTML dos diálogos,
  o CSS (`.dialog*`) e o wiring no `app.js`.
- **Layout vertical e centralizado.** A `.topbar` (marca + botão numa linha) deu
  lugar a uma coluna centralizada (`.app` com `max-width` 1080px): a marca
  "Concorde" virou título centralizado acima do player, e o **player ficou um
  pouco maior** (ocupa a largura da coluna, ~1048px contra ~930 antes) e
  centralizado.
- **Botão de transmitir sob o player.** Saiu do topo; agora fica logo abaixo do
  player, **alinhado à direita** dele (`.stage` virou coluna e `.stage__actions`
  usa `justify-content: flex-end`).
- **Formulários lado a lado, mesma altura.** "Criar sala" e "Entrar em uma sala"
  ficam num grid de duas colunas iguais (`.lobby-forms`). O grid estica os dois
  cards para a mesma altura e o botão de cada um é fixado embaixo
  (`margin-top: auto`), então o form mais curto ("Entrar") ganha um espaço vazio
  proposital e os dois botões alinham na mesma linha. Empilham em telas ≤640px.
- Room-view centralizado com largura máxima; rodapé fixado embaixo
  (`margin-top: auto`).

**Verificado:** `node --check` no `app.js`, `go build`/`vet` limpos, e smoke test
do servidor — index servido com marca centralizada, `.stage__actions` e
`.lobby-forms`, e sem os diálogos; CSS idem.

**Não verificado aqui (precisa de você, navegador real):** o visual final do novo
layout. Roda local e me diz se o tamanho e o alinhamento do player e o
espaçamento dos forms ficaram do teu gosto.

**Próximo:** Fase B segue aberta — mais ajustes conforme teu feedback.

---

## Sessão 5 — Polimento e teste ponta a ponta (2026-09-15)

Branch: `main`.

Fecha a **Fase A**: a primeira versão que dá pra ver rodando. Todo o trabalho é
de UX e robustez do frontend — o backend não mudou.

**Tela cheia (para quem assiste):** botão ⛶ na barra de controles do vídeo (só
aparece para o viewer, ao lado do volume). Usa a Fullscreen API na **janela do
vídeo inteira** (`#video-window`), não só no `<video>`, para os controles
continuarem acessíveis em tela cheia (a barra fica sempre visível em fullscreen).
A `aspect-ratio` é removida em fullscreen e o vídeo faz letterbox no preto
(`object-fit: contain`). `fullscreenchange` sincroniza o rótulo/`aria-label` do
botão; Esc sai (nativo).

**Confirmação no botão Transmitir:** os dois caminhos passam por um `<dialog>`
nativo (on-theme, com `::backdrop`):
- Antes de **começar**: lembrete para escolher uma **aba** e marcar
  "Compartilhar áudio da aba" — a causa mais comum de transmissão sem som. O
  `getDisplayMedia` é chamado no clique de "Continuar", preservando o gesto do
  usuário (a chamada não pode vir depois de um `await`).
- Antes de **parar**: confirmação, já que parar encerra a transmissão para todos.

O botão também ganha um estado visual "ao vivo" vermelho (`.is-live`) enquanto
transmite.

**Tratamento de erros:**
- **Aviso de navegador:** banner dispensável no topo para quem não está no
  Chrome/Edge (Firefox/Opera são avisados sobre possível falta de áudio, como o
  plano pede). Detecção por `navigator.userAgentData.brands` (exclui "Opera"
  explicitamente; aceita "Google Chrome"/"Microsoft Edge"), com fallback por UA
  string quando UA-CH não existe (ex.: Firefox).
- **Toasts** (`#toasts`, `aria-live`): avisos transitórios e independentes de
  tela para — falha ao iniciar a captura; captura indisponível (origem insegura
  ou sem suporte, via guarda `navigator.mediaDevices?.getDisplayMedia`); conexão
  P2P falha (só para o viewer, que sem ela fica sem nada na tela); conexão com o
  servidor perdida; e **sala encerrada** (antes ia para o erro do lobby, agora é
  toast). Erros de formulário (admin/senha/sala) seguem inline no lobby via
  `showError`.

**Polimento de tema:** `:focus-visible` para navegação por teclado, o estado
`.is-live`, e estilos on-theme para banner, diálogos e toasts. Novas CSS vars
`--danger`/`--danger-hover`/`--error-text` (a cor de erro que já existia virou
variável e foi reaproveitada no `.error` e no `.btn-danger`).

**Detalhe de CSS:** `.video-controls` virou flex (volume à esquerda, tela cheia
à direita); como isso adiciona um `display` explícito, foi preciso um
`.video-controls[hidden] { display: none }` para o atributo `hidden` (que o
preview do broadcaster usa) continuar escondendo a barra — mesmo cuidado que já
existia no `.panel__view`.

**Verificado:** `go build`/`vet`/`test ./...` limpos (backend intacto);
`node --check` nos dois JS; smoke test do servidor em porta alternativa — index
servido já com `#browser-warning`, `#fullscreen-btn`, `#start-dialog`,
`#stop-dialog` e `#toasts`; CSS e JS servidos com as peças novas; `/healthz` ok.

**Não verificado aqui (precisa de você, navegador real):** o visual em si e o
fluxo ponta a ponta — tela cheia, os dois diálogos, a captura de áudio da aba e
a transmissão P2P. Mesma limitação de sempre (sem navegador aqui).

**Próximo:** Fase B — você roda localmente, vê a primeira versão e me passa o
feedback. O passo a passo de teste está no `Plano.md`.

---

## Sessão 4 — TTL + tela de admin (2026-09-15)

Branch: `session-2-rooms` (continuação).

Duas entregas: salas ociosas se encerram sozinhas (TTL) e um **painel de admin**
em `/admin.html` para ver as salas abertas e fechá-las na hora.

**TTL (reaper de salas ociosas):**
- `Room.broadcasting bool` novo: a sala fica **imune ao reaper enquanto o admin
  está transmitindo**. O `lastActive` agora também é atualizado ao começar/parar
  a transmissão, além das mudanças de participantes.
- `Hub.StartReaper(ctx)` (chamado no `main`) roda um `time.Ticker` a cada `TTL/4`
  (mínimo 15s) e chama `reapIdle`, que fecha toda sala **não transmitindo** e
  ociosa há ≥ `ROOM_TTL_MINUTES` (default 10). TTL ≤ 0 desliga o reaper.
- `closeRoomLocked(room, reason)`: novo caminho que encerra a sala inteira
  (diferente do `removeLocked`, que tira um só participante). Notifica todos com
  `room-closed {reason}`, desanexa e apaga a sala.
- Regra prática: sala criada mas sem ninguém transmitindo por X min → fechada;
  transmissão parada reinicia o relógio de X min.

**Sinais de transmissão (frontend → servidor):** `broadcast-start` /
`broadcast-stop` (só o broadcaster; ignorados de outros). O `app.js` os envia em
`startBroadcast`/`stopBroadcast`. Servem só ao TTL — a mídia continua P2P.

**Painel de admin (`/admin.html` + `admin.js`):**
- Auth via **HTTP Basic** contra as credenciais de admin (`crypto/subtle` para
  comparação em tempo constante). Login manda `Authorization: Basic …` em toda
  request; nada é persistido além da aba (fica em memória).
- Endpoints novos no `main`: `GET /admin/rooms` → `{rooms:[{id, participants,
  broadcasting, createdAt, lastActive, idleSeconds}]}` (sem senhas), e
  `POST /admin/rooms/close {roomId}` → 204 / 404. Ambos em `internal/signaling/
  admin.go` (`ServeAdminRooms`, `ServeAdminClose`, `CloseRoom`, `snapshotRooms`).
- Página lista as salas (mais nova primeiro), mostra 🔴 quem está transmitindo e
  há quanto tempo cada uma está ociosa, com botão "Fechar" por sala e
  auto-refresh a cada 5s. Link discreto "admin" no rodapé do `index.html`.

**Frontend do usuário:** `app.js` trata `room-closed` (sai da sala e mostra o
motivo: fechada pelo admin ou por inatividade).

**Verificado:** `go build`/`vet`/`test ./...` limpos. Testes novos
(`hub_test.go`): reaper fecha sala ociosa mas poupa a que transmite e a recém-
criada; `room-closed(idle)` chega ao evictado; TTL 0 desliga; `/admin/rooms`
exige auth, lista salas com auth; `/admin/rooms/close` despeja e notifica todos
com `closed-by-admin`. Smoke test no servidor real (porta alternativa):
`/healthz`, `/admin.html` (200), `/admin/rooms` 401 sem auth / `{"rooms":[]}`
com auth, close de sala inexistente → 404.

**Não verificado aqui (precisa de você, navegador real):** o painel visual em si
e o encerramento por inatividade end-to-end — mesma limitação de sempre (sem
navegador aqui). O núcleo é coberto pelos testes.

**Próximo:** Sessão 5 — polimento e teste ponta a ponta (tema, confirmação do
botão Transmitir, tela cheia para quem assiste, tratamento de erros).

---

## Sessão 3 — Núcleo WebRTC (2026-09-13)

Branch: `session-2-rooms` (continuação).

Caminho de mídia ligado: o admin captura uma **aba** com `getDisplayMedia` e
transmite direto para cada espectador via `RTCPeerConnection`. A mídia nunca
passa pelo servidor — ele só faz o relay dos `signal` (SDP/ICE) já pronto na
Sessão 2. Tudo é frontend; o backend não mudou.

**Topologia:** só o broadcaster envia mídia, então é uma **estrela**, não mesh.
O broadcaster é sempre quem faz a `offer`; o viewer só responde. Como os papéis
são fixos, não há "glare" — dispensa perfect-negotiation.

**Feito (`public/`):**
- `app.js`: camada WebRTC.
  - `RTCPeerConnection` por peer (`state.pcs`), STUN público do Google
    (`stun:stun.l.google.com:19302`). Sem TURN (decisão do plano).
  - Broadcaster: `startBroadcast` → `getDisplayMedia` (vídeo `ideal 1920×1080
    @30fps`, áudio com `echoCancellation`/`noiseSuppression`/`autoGainControl`
    desligados para não tratar anime/música como voz). `offerTo(peer)` anexa as
    tracks e manda a offer. Botão vira "Parar transmissão"; encerrar o
    compartilhamento pela UI do navegador (evento `ended`) também para.
  - Viewer: ao receber `offer`, cria a PC, `ontrack` → exibe o stream, responde
    com `answer`.
  - **ICE trickle** com buffer: candidatos que chegam antes da remote description
    ficam em `pc.pendingCandidates` e são drenados por `flushCandidates`.
  - Ciclo de vida: `peer-joined` durante transmissão → offer ao novo viewer;
    `peer-left` → fecha a PC (viewer sem PCs → esconde o vídeo); sair da sala /
    socket fechar → `stopMedia` (para tracks, fecha PCs, limpa o vídeo).
  - Botão "Transmitir" só aparece para o broadcaster (`updateBroadcastButton`).
- `index.html`: `<video>` + placeholder + barra de volume sobre o vídeo.
- `styles.css`: `.video`, `.video-controls` (aparece no hover/foco), `.volume`
  com `<input type=range>` (`accent-color`). Preview do broadcaster fica **mudo**
  (sem eco); só o viewer vê/usa o controle de volume.

**Protocolo de `signal.payload`** (opaco pro servidor, definido no frontend):
`{kind:"offer"|"answer", sdp}` e `{kind:"ice", candidate}`.

**Verificado:** `go build`/`vet`/`test ./...` limpos (backend intacto);
`node --check public/app.js` ok; servidor serve o index já com `#video`,
`#video-controls` e `#volume`.

**Não verificado aqui (precisa de você, navegador real):** o fluxo P2P de ponta
a ponta e, principalmente, a **captura de áudio da aba** (flag do plano). Ao
rodar: abrir 2 abas Chrome, numa criar sala como admin e clicar "Transmitir"
escolhendo **uma aba** com a opção *"compartilhar áudio da aba"* marcada; na
outra, entrar com o código+senha e confirmar vídeo + som + controle de volume.

**Próximo:** Sessão 4 — TTL de sala ociosa + tela de admin (ver salas e fechar).

---

## Sessão 2 — Salas, senha e admin (2026-09-13)

Branch: `session-2-rooms`.

Núcleo de sinalização: admin cria sala, amigos entram por senha, limite de 3, e
o **relay de mensagens WebRTC já está pronto** (o `getDisplayMedia`/mídia em si
fica para a Sessão 3). Mídia nunca passa pelo servidor.

**Feito:**
- Novo pacote `internal/signaling/` (o `handleWS` do scaffold saiu do `main.go`):
  - `hub.go`: `Hub` com um único `sync.Mutex` guardando `rooms` e a participação
    dos clientes (simples e suficiente para poucas salas pequenas). Métodos
    `ServeWS`, `createRoom`, `joinRoom`, `leaveRoom`, `relay`, `removeLocked`.
  - `client.go`: `Client` com read loop (parse + dispatch) e um write loop
    dedicado drenando um canal `send` bufferizado (32). Envio é **não-bloqueante**
    (`enqueue`) para nunca travar o hub segurando o lock; overflow só loga e
    descarta (não acontece em salas de 3).
  - `room.go`: `Room` (id, senha, clients, `createdAt`, `lastActive`). O
    `lastActive` já é atualizado, pronto para o TTL da Sessão 4.
  - `messages.go`: protocolo JSON (in/out) + códigos de erro estáveis + papéis
    (`broadcaster`/`viewer`).
  - `ids.go`: `randomCode` via `crypto/rand`, alfabeto sem caracteres ambíguos
    (código de sala de 6, id de cliente de 10).
- **Papéis:** quem cria a sala é `broadcaster` (é a tela do admin que assistimos);
  quem entra é `viewer`. Alinha com a monetização (host = feature paga depois).
- **Protocolo WebSocket** (JSON, campo `type`):
  - cliente→servidor: `create-room {user,password,roomPassword}`,
    `join-room {roomId,roomPassword}`, `signal {to,payload}`, `leave-room`.
  - servidor→cliente: `hello`, `room-created {roomId,selfId,role}`,
    `joined {roomId,selfId,role,peers[]}`, `peer-joined {peerId,role}`,
    `peer-left {peerId}`, `signal {from,payload}`, `error {code,message}`.
  - erros: `bad-admin`, `bad-room-password`, `room-not-found`, `room-full`,
    `not-in-room`, `peer-not-found`, `bad-message`.
- **Frontend** (`public/`): painel lateral com formulário de criar sala (admin)
  e de entrar por senha; visão da sala mostra código, papel e lista de peers;
  botão "Sair da sala". `app.js` faz o handshake de sinalização e reflete o
  estado (mensagens `signal` já chegam, apenas logadas até a Sessão 3). Estilos
  novos (`.card`, `.field`, `.btn-secondary`, `.room-code`, etc.), responsivo.

**Como rodar:** igual à Sessão 1 (`go run ./cmd/server` → `localhost:3000`).
Abrir duas abas: numa criar sala como admin, noutra entrar com o código+senha.

**Verificado:**
- `internal/signaling/hub_test.go` (integração via `httptest` + cliente WS):
  auth de admin, senha da sala obrigatória, fluxo de entrada + notificação de
  peer, senha errada, sala cheia (4º rejeitado), relay de `signal` entre peers,
  `peer-left` ao sair. `go build`, `go vet` e `go test ./...` limpos.
  *(`-race` não roda aqui: falta compilador C 64-bit no ambiente Windows.)*
- Servidor sobe em porta alternativa, `/healthz` → `{"ok":true}`, index (200)
  já contém o painel novo.

**Próximo:** Sessão 3 — Núcleo WebRTC (`getDisplayMedia`, P2P transmissor→
espectadores, exibir vídeo, controle de volume). O relay de `signal` já existe.

---

## Sessão 1 — Scaffold (2026-09-12)

Branch: `session-1-scaffold`.

**Backend em Go** (decidido no meio da sessão: mais familiaridade do usuário e
consistência com os outros projetos dele, também em Go; troca barata no scaffold e o
frontend não muda). O scaffold Node inicial foi descartado antes do merge.

**Feito:**
- Módulo Go `github.com/arthurdepina/concorde` (Go 1.25). Dep única:
  `github.com/coder/websocket`.
- `cmd/server/main.go`: HTTP + WebSocket no mesmo servidor (`net/http` +
  `http.FileServer` para `public/`). Rotas: `GET /healthz`, WS em `/ws` (só
  esqueleto: manda `hello` e loga mensagens — sem salas/auth/WebRTC ainda).
- `internal/config/config.go`: lê env com defaults (`PORT`, `ADMIN_USER`,
  `ADMIN_PASSWORD`, `ROOM_TTL_MINUTES`, `MAX_PARTICIPANTS`); mini-loader de
  `.env` embutido (sem dependência externa). Exemplo em `.env.example`.
- `public/`: `index.html` + `styles.css` (tema cinza-azulado via CSS vars) +
  `app.js` (conecta no `/ws` e reflete o status de conexão; botão "Transmitir"
  ainda é placeholder). **Frontend inalterado desde o scaffold Node.**

**Como rodar:** `go run ./cmd/server` → abrir `http://localhost:3000`.
Build: `go build -o bin/concorde ./cmd/server`.

**Verificado:** servidor sobe e loga a URL; `/healthz` retorna `{"ok":true}`;
index é servido; WS conecta e recebe `{"type":"hello","from":"concorde"}`.

**Estrutura:**
```
cmd/server/            main.go
internal/config/       config.go
public/                index.html, styles.css, app.js
go.mod, go.sum, .env.example, .gitignore
```

**Nota (Windows):** `go run` deixa o binário filho rodando; ao testar, matar o
processo que ocupa a porta 3000 antes de subir de novo (senão dá erro de bind).

**Próximo:** Sessão 2 — Salas, senha e login de admin (ver `Plano.md`).

---

## Sessão 0 — Planejamento (2026-09-12)

**Feito:**
- Lido o `spec-pt-br.md`, o `task.md` e o `DEPLOY.md`.
- Criado `.gitignore` (inclui `Plano.md`, que nunca deve ser commitado; Node,
  `.env`, build output, arquivos de editor/OS).
- Criado `Plano.md` (não commitado) com visão geral, arquitetura, regras de
  sessão, roadmap por sessões, flags e nota de monetização.
- Criado este `Changelog.md`.

**Decisões técnicas:**
- Arquitetura: **WebRTC P2P (mesh)** para até 3 pessoas — mídia direto entre
  navegadores, sem custo de banda em servidor.
- **Servidor de sinalização** em Node + WebSocket (troca SDP/ICE, salas, senhas,
  login de admin, limite de 3, TTL). **STUN** público grátis; **TURN** só se
  necessário (decisão de deploy).
- Dev local: um app Node serve o frontend estático e a sinalização; abrir em
  `localhost`. Deploy pretendido em `share.example.com`.

**Decisões do usuário (flags resolvidas):**
- Transmissão por **aba** do navegador (garante áudio no Chrome).
- Alvo Chrome/Edge; para outros navegadores, mostrar aviso de possível problema
  de som; não testar Firefox/Opera por ora.
- **TURN:** começar sem (só STUN); adicionar auto-hospedado só se algum amigo
  não conectar.
- **Custo esperado:** ~R$0/mês hospedando de casa (Cloudflare Tunnel) + STUN
  grátis; ou ~US$5/mês se quiser cloud sem manutenção. Domínio já é pago.

**Em aberto (Fase C):** host de produção; endereço (`example.com/share`
preferido pelo usuário vs `share.example.com` recomendado).

**Próximo:** Sessão 1 — Scaffold (ver `Plano.md`).
