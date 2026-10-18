# Concorde

Private screen sharing for a few friends, straight between browsers, on a
server you host yourself.

One person shares a tab, a window or the whole screen, and up to two others
watch it in Full HD with sound. Video and audio travel peer to peer over WebRTC;
the server only introduces the browsers to each other. There is no chat and no
voice: it does one thing, a fast and good-looking stream, and stays out of the
way. It was made for watching shows together with friends who live far apart.

> The interface is in Brazilian Portuguese.

## Features

- **Private rooms.** Broadcasters log in with an account; viewers need only the
  room link (or code), plus the room password if the broadcaster set one.
- **Small rooms:** up to 3 people (broadcaster + 2 viewers) by default.
- **Player shortcuts:** <kbd>F</kbd> toggles fullscreen, <kbd>M</kbd> mutes.
- **Quality:** Full HD target at up to 30 fps, up to 8 Mb/s of video per viewer,
  stereo Opus audio at 128 kb/s. A stats overlay shows resolution and frame
  rate, and tells the broadcaster what is limiting each viewer (bandwidth or
  CPU).
- **Only the audio of what is shared.** A tab shares the tab's sound; a window
  shares only the sound of the app that owns it (Chrome 141+). A whole screen
  goes out silent: its only audio is the system mix, which would carry
  everything playing on the computer, voice calls included.
- **Survives hiccups.** If the signaling connection drops, the video keeps
  playing while the page reconnects and takes back its seat. Reloading the page
  puts you back in the room.
- **Idle rooms close themselves** (10 minutes with nobody broadcasting, by
  default).
- **Admin panel** at `/admin.html`: open rooms (and closing them), live
  metrics, and the broadcaster accounts.
- **Admin and account passwords are stored only as bcrypt hashes.** Room
  passwords live in memory only, for as long as the room exists. A visitor is
  locked out for a while after 3 wrong passwords in a row. Wrong room codes
  count too, since a room without a password has only its code to keep
  strangers out.
- **Small footprint:** a single Go binary plus static HTML/CSS/JS, with no
  database and no frontend build step.

## How it works

```
 broadcaster ════ video + audio (WebRTC, peer to peer) ════▶ viewers
      │                                                        │
      └──────────── signaling (WebSocket /ws) ── server ───────┘
```

The Go server serves the frontend and relays WebRTC signaling over a WebSocket.
Media never passes through it, so it needs very little CPU or bandwidth.

Things to know:

- **No TURN relay.** Browsers find each other through a public STUN server.
  Viewers behind very strict NATs (some mobile and CGNAT networks) may fail to
  connect; the admin panel counts how often that happens.
- **The broadcaster uploads one stream per viewer.** At the 8 Mb/s cap, two
  viewers can take up to ~16 Mb/s of upload.
- **Browsers:** Chrome and Edge on desktop. Other browsers get a warning, since
  audio capture may not work there.
- **HTTPS is required.** Browsers only allow screen capture on secure origins
  (HTTPS, or `localhost` while developing).

## Quick start

Requires [Go](https://go.dev/dl/) 1.26 or newer.

```sh
git clone https://github.com/arthurdepina/concorde
cd concorde
cp .env.example .env
go build -o bin/concorde ./cmd/server
./bin/concorde hash   # type the admin password, paste the printed line into .env
./bin/concorde
```

On Windows the binary is `bin\concorde.exe`. Run it from the repository folder:
`public/` and `.env` are looked up relative to the working directory. If no
`ADMIN_PASSWORD_HASH` is set, the server makes up a strong admin password for
that run and prints it in the log.

Open <http://localhost:3000>, log in as the admin, optionally set a room
password, and share the room link. Friends open the link and type the room
password, if there is one.

While nothing is on air, the player can show an image of your choice above its
"no broadcast yet" line: put it at `public/placeholder.webp`. The file is
gitignored, so each instance keeps its own; without one, only the text shows.

## Broadcaster accounts

Accounts let friends open rooms of their own. Create them in the admin panel
(the password is shown once, to pass along) or from the command line:

```sh
concorde account add <name>      # asks for the password
concorde account list
concorde account passwd <name>
concorde account rm <name>
```

They are kept, as bcrypt hashes, in the file named by `ACCOUNTS_FILE`. In
production, keep that file outside the repository folder and include it in your
backups. Accounts can broadcast but cannot open the admin panel; the admin is
configured only through `ADMIN_USER` and `ADMIN_PASSWORD_HASH`.

## Configuration

Set these in the environment or in a `.env` file in the working directory (see
[`.env.example`](.env.example)).

| Variable | Default | What it does |
|---|---|---|
| `PORT` | `3000` | HTTP port. |
| `ADMIN_USER` | `admin` | Admin login name. |
| `ADMIN_PASSWORD_HASH` | (none) | bcrypt hash from `concorde hash`. A plain `ADMIN_PASSWORD` is refused at startup. |
| `ACCOUNTS_FILE` | `accounts.json` | Where broadcaster accounts are stored. |
| `ROOM_TTL_MINUTES` | `10` | How long a room may stay open with nobody broadcasting. |
| `MAX_PARTICIPANTS` | `3` | People per room, broadcaster included. |
| `ALLOWED_ORIGINS` | (empty) | Comma-separated public hostnames accepted as the WebSocket origin. Empty means same-origin only. |
| `STATS_INTERVAL_MINUTES` | `15` | Interval of the metrics line in the log. `0` turns it off. |
| `WS_PING_SECONDS` | `30` | WebSocket keepalive, so proxies don't close the quiet socket. `0` turns it off. |
| `RESUME_GRACE_SECONDS` | `30` | How long a dropped participant's seat is held. `0` turns it off. |
| `LOGIN_MAX_FAILURES` | `3` | Wrong passwords (or room codes) in a row before a visitor is locked out. `0` turns it off. |
| `LOGIN_LOCKOUT_MINUTES` | `15` | How long the lockout lasts. |

## Self-hosting with Cloudflare Tunnel

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/)
puts the server online with HTTPS from any machine, a home computer included,
without opening ports on the router. It is free; you need a domain on
Cloudflare.

1. Install [`cloudflared`](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/)
   and log in:
   ```sh
   cloudflared tunnel login
   cloudflared tunnel create concorde
   ```
2. Create `~/.cloudflared/config.yml` (on Windows,
   `%USERPROFILE%\.cloudflared\config.yml`):
   ```yaml
   tunnel: concorde
   credentials-file: /home/you/.cloudflared/<tunnel-id>.json
   ingress:
     - hostname: concorde.example.com
       service: http://localhost:3000
     - service: http_status:404
   ```
3. Point the hostname at the tunnel:
   ```sh
   cloudflared tunnel route dns concorde concorde.example.com
   ```
4. In `.env`, accept the public hostname as the WebSocket origin:
   ```
   ALLOWED_ORIGINS=concorde.example.com
   ```
5. Start the server, then the tunnel:
   ```sh
   cloudflared tunnel run concorde
   ```
   Check it with `curl https://concorde.example.com/healthz`.

On a machine meant to stay up, run both as services (for example with
`systemd`) so they come back after a reboot.

**Behind another reverse proxy.** That works too, with one caveat: to tell
visitors apart for the login lockout, the server trusts the `CF-Connecting-IP`
header on requests that come from the same machine (that is, from
`cloudflared`). Behind a different proxy on the same machine, every visitor
appears as `127.0.0.1` and they all share one lockout counter.

## Development

```sh
go test ./...
```

The frontend is plain HTML, CSS and JavaScript in `public/`; edit and reload.
[`Changelog.md`](Changelog.md), in Portuguese, records every development
session in detail: what changed, why, and how it was verified.

## License

[MIT](LICENSE) © 2026 Arthur de Pina
