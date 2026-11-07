# Fern

Fern is a lightweight OSC (Open Sound Control) client for VRChat. It keeps your chatbox
filled with useful, personalised information and gives you a small panel to control it.

## Features

- **Now playing with playback time** — reads the Windows media sessions (the same data the
  Windows volume flyout uses), so Spotify, Chrome, VLC and friends show up with title,
  artist, elapsed time and track length. No Spotify login, no API keys, no extra process.
  A window-title fallback is used if Windows reports no session.
- **Choose the line order** — every module can be toggled and dragged into any order.
  Lines are joined with a separator you pick (new line by default).
- **Send messages on the spot** — type a message in the app and push it to the chatbox,
  either for a few seconds or pinned until you clear it.
- **Custom text rotation** — add, edit, delete and reorder messages, set how long each one
  stays up, and optionally shuffle them.
- **AFK timer** — driven by VRChat's outgoing OSC (movement / voice) and active window
  changes, with a configurable threshold and label.
- **VRChat logs** — joins, leaves, an activity feed and a history of everything Fern sent.
- **Settings that stick** — every option is written to `config.json` automatically.
- **Fast** — no CDN, no web fonts, no frameworks in the UI; the VRChat log is tailed
  incrementally instead of being re-read every tick, and media/CPU/RAM are sampled on
  background timers instead of on every send.

## Modules

| Module | Shows |
| --- | --- |
| AFK | Time since you were last active |
| Loading | Growing dots: `.`, `..`, `...` — or your own text with dots after it |
| Time | Clock (12/24h, optional seconds and label) |
| Text | Your rotating messages |
| Memory | RAM used / total |
| CPU | CPU name, core count and load |
| Media | Track, artist, progress and `elapsed/total` |
| Active window | Title of the focused window |
| VRChat log | Last join / leave |

## Building

```powershell
./build.ps1
```

or directly:

```powershell
go build -ldflags "-H windowsgui" ./Fern
```

Requirements: Go 1.23+, a C toolchain for `webview_go` (MinGW-w64 on Windows) and the
WebView2 runtime.

## Notes

- Fern sends to `127.0.0.1:9000` and listens on `127.0.0.1:9001`; both are configurable.
- Only run one copy at a time — two instances will fight over the chatbox and the OSC port.
- UI is served locally on the first free port from 1932; open it in a browser with
  `--debug` for devtools.
