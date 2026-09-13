# Changelog — Concorde

Histórico do que foi feito em cada sessão, com detalhes técnicos. Sessões novas
leem este arquivo + o `Plano.md` antes de começar. Mais recente no topo.

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
