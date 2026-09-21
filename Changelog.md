# Changelog — Concorde

Histórico do que foi feito em cada sessão, com detalhes técnicos. Sessões novas
leem este arquivo + o `Plano.md` antes de começar. Mais recente no topo.

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
