# Fern ｢フェルン｣

Fern is a Windows chatbox client for VRChat. It keeps your chatbox filled with a rotating
set of lines — now playing, clock, RAM and CPU, the active window, joins and leaves, your
own messages — and gives you a local panel to arrange and configure every one of them.

It talks to VRChat over OSC, so there is no mod, no account and no API key involved, and
the whole interface is served by the same executable with its fonts and icons embedded.

## Features

- **Forty-one chatbox lines** — VRChat, music, system, SteamVR, streaming and app lines, plus
  three custom lines of your own (see [Modules](#modules)). Each one can be switched on or
  off, dragged into any order, given a drop priority for when the chatbox is too long, and
  shown always, only in VR or only on desktop.
- **Per-line settings** — every module has its own label, visibility and formatting options:
  templates and placeholders, which parts of the track to show, timer and AFK styles, Unicode
  lettering, character limits and more.
- **Chatbox motion and pages** — scroll, typewriter or blink effects per line, and named
  pages of lines that take turns, all kept to VRChat's rate limit.
- **Now playing from Windows** — reads the same media sessions the Windows volume flyout
  uses, so Spotify, Chrome, VLC and friends show up with title, artist, album, progress bar
  and synced lyrics. Optional Spotify and Last.fm sign-ins add liked songs, shuffle, devices,
  play counts and controls.
- **Send a message right now** — type in the panel and push it to the chatbox, with live
  typing, history, an emoji picker, AI rewrite and translate tools, dictation, text to speech
  and a word filter. A global shortcut sends whatever you copied.
- **Text rotation** — add, edit, delete and reorder messages, group them, heart the ones in
  the rotation, and shuffle them. Scheduled messages go out at a set time or every few
  minutes.
- **Profiles** — save the whole chatbox setup under a name and switch from the panel, the
  tray, a global shortcut, a rule or the avatar you change into.
- **Rules** — "when this happens, do that": worlds, avatars, avatar parameters, time of day,
  AFK, players, timers, sensors, stream events, voice commands and more can switch profiles
  and lines, send messages, set parameters, press keys, open apps or drive OBS, Spotify and
  Voicemod. Do not disturb hides everything but the lines you allow.
- **Avatar automation** — profiles and saved parameters per avatar, media keys, VRChat
  events, time and PC load as parameters, timer and counter parameters (headpats, boops),
  controller input and haptics, all on one Avatar page with live values.
- **OSC done properly** — OSCQuery so other OSC apps keep port 9001, forwarding of what VRChat
  sends to up to eight other apps, up to two extra chatbox outputs, and a pause in club and
  event worlds on the community blocklist.
- **Streaming tools** — Twitch chat and alerts, TikTok Live, Kick, YouTube and OBS lines, OBS
  controls, and a chatbox overlay for an OBS Browser source.
- **Control API** — drive Fern from a Stream Deck, Touch Portal, Streamer.bot, PowerShell or
  curl over HTTP or a WebSocket, with a token and answers only for programs on this PC.
- **AFK detection** — watches VRChat's outgoing OSC (movement and voice), other OSC, window
  changes and optionally keyboard and mouse, with a configurable threshold, style and timer
  format.
- **VRChat logs** — joins, leaves, screenshots, world loads, the current world and session
  stats, and a history of everything Fern sent that can be saved as .txt or .csv, tailed
  incrementally instead of re-read every tick.
- **Settings that stick** — everything is written to `%LOCALAPPDATA%\Fern\config.json`,
  so it survives updates and moving the executable, and old config files pick up new
  options with sensible defaults. Backups (automatic before updates and imports, or by hand)
  leave out API keys and tokens, which are stored encrypted in `secrets.dat`.
- **First-run setup** — checks that VRChat's OSC is on, sends a test message and turns on a
  starter set of lines.
- **Self-contained UI** — no CDN, no web fonts, no framework; the stylesheet, fonts, icons
  and emoji are embedded in the binary.

## Modules

| Module | Shows |
| --- | --- |
| AFK | Time since you were last active, or just a label |
| VRChat log | Last join, leave, screenshot or world load |
| VRChat world | World name, players and peak, instance type, region and time in the instance |
| Heart rate | Live BPM from Pulsoid, HypeRate or any JSON source, with zones and trend |
| Counters | Headpats, boops or anything an avatar parameter counts, per session and all time |
| Media | Track, artist, album, progress bar and `elapsed/total`, plus Spotify extras |
| Lyrics | The line being sung, from LRCLIB or your own `.lrc` files |
| Last.fm | What you are scrobbling, with play count and loved mark |
| Text | Your rotating messages |
| Loading | Growing dots (`.`, `..`, `...`) with optional text in front |
| Timer | Countdown, stopwatch or Pomodoro |
| Scheduled messages | Messages at a set time or every few minutes |
| Time | Clock, 12/24h, any time zone, a second clock and countdowns to dates |
| Weather | Conditions, temperature, humidity and wind from Open-Meteo |
| Calendar | Your next or current event from iCal links or `.ics` files |
| Prices | Exchange rates and crypto prices for up to 8 pairs |
| Memory | RAM used, total and percentage |
| CPU | CPU name, core count, usage, and temperature, power, clock and fan from sensors |
| GPU | Load and video memory, and temperature, power, clock and fan from sensors |
| Network | Download and upload speed, peak and totals |
| Sensors | Temperatures, power, clocks and fans you pick from HWiNFO or LibreHardwareMonitor |
| Ping | Round trip, average and packet loss to a host you choose |
| Disk usage | Used, size, free and percent for the drives you pick |
| Uptime | How long the PC and Fern have been running |
| PC battery | Laptop charge, charging mark and time left |
| Active window | The focused app or window title, renamed or hidden per app |
| Tracker battery | Headset, controller and tracker charge from SteamVR |
| VR performance | Frame rate, reprojection, dropped frames and frame times from SteamVR |
| Discord | Voice channel, who is talking and your mute state |
| Twitch | Live or offline, viewers, game, title and followers |
| Twitch chat | Your newest chat message and stream alerts |
| TikTok Live | Viewers, likes, follows and gifts via TikFinity or TikTok-Live-Connector |
| Kick | Live status, viewers and followers |
| YouTube | Live status, viewers and subscribers |
| OBS | Live and recording time and the current scene |
| Soundpad | The sound you just played |
| Voicemod | Active voice, last sound, and when you're muted |
| Steam | The game you're playing and for how long |
| Custom line 1–3 | Text from the control API, a rule, a command or script, or a web address |

## Download

Grab `Fern.exe` from [Releases](https://github.com/MewishMC/Fern/releases) and run it.
Fern needs Windows 10 or 11 (x64) and the WebView2 runtime, which ships with Windows 11 and
current Windows 10 installs.

Fern checks for new releases while it runs. When one is out, an **Update** button appears at
the bottom of the sidebar; it downloads the new version and restarts Fern on it. You can also
keep `update.exe` next to `Fern.exe` and start that instead, which updates Fern before
launching it.

## Build from source

Requirements:

- [Go](https://go.dev/dl/) 1.23 or newer
- MinGW-w64 (provides `gcc` for cgo and `windres` for the icon/manifest resource)
- The WebView2 runtime

```bat
git clone https://github.com/MewishMC/Fern.git
cd Fern
build.bat
```

`build.bat` compiles the Windows resource into `Fern/Fern.syso`, then builds `Fern.exe` and
`update.exe`. To run straight from source without a release build, use `run.bat`, or:

```bat
go run ./Fern
```

If you build with `go build ./Fern` directly, everything still works but the executable has
no embedded icon or DPI manifest, because those come from the `.syso` that `windres` writes.

## Usage

1. Start Fern. The panel opens automatically; the UI is served at
   `http://127.0.0.1:1932/` (or the next free port) so you can also open it in a browser.
2. In VRChat, make sure OSC is enabled (Action Menu → Options → OSC → Enabled).
3. Arrange the lines on the **Lines** page, then configure each module on its own page.

Fern sends chatbox input to `127.0.0.1:9000` (configurable in `config.json`) and listens on
`127.0.0.1:9001` for VRChat's outgoing OSC, which is what drives AFK detection. Run only one
copy at a time — two instances fight over the chatbox and the listening port.

Start Fern with `--debug` to open the WebView with developer tools enabled.

## Configuration

Settings live in `%LOCALAPPDATA%\Fern\config.json`, created on first launch and rewritten
whenever you change a setting. If an older `config.json` sits next to the executable, Fern
moves it there on first launch and keeps the original as `config.json.old`. Delete the file
in `%LOCALAPPDATA%\Fern` to reset everything to defaults; unknown or missing fields in an
older file fall back to the defaults rather than failing to load.

## Project layout

```
Fern/          the application
  main.go      OSC send loop and startup
  server.go    local HTTP + websocket server for the panel
  UI/          embedded page, stylesheet, fonts and icons
  modules/     one file per chatbox line, plus config and workers
update/        small standalone updater that swaps in a newer release
build.bat      build both executables
run.bat        run from source
```

## Contributing

Bug reports and pull requests are welcome — see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Fern is released under the [MIT License](LICENSE).

Bundled third-party assets keep their own licences:

- Font Awesome Free icons — [LICENSE](Fern/UI/assets/fa/LICENSE.txt)
- Noto Emoji font — [OFL 1.1](Fern/UI/assets/fonts/LICENSE.txt)
- CodeMirror 5 (custom CSS editor) — [MIT](Fern/UI/assets/codemirror/LICENSE.txt)
- OpenVR SDK v2.15.6 `openvr_api.dll` (SteamVR battery and performance lines), embedded in Fern.exe — Copyright (c) 2015, Valve Corporation, [BSD 3-Clause](Fern/modules/openvr/LICENSE-openvr)
