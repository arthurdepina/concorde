# Changelog — Concorde

Histórico do que foi feito em cada sessão, com detalhes técnicos. Sessões novas
leem este arquivo + o `Plano.md` antes de começar. Mais recente no topo.

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
