# Fern ｢フェルン｣

Fern is a Windows chatbox client for VRChat. It keeps your chatbox filled with a rotating
set of lines — now playing, clock, RAM and CPU, the active window, joins and leaves, your
own messages — and gives you a local panel to arrange and configure every one of them.

It talks to VRChat over OSC, so there is no mod, no account and no API key involved, and
the whole interface is served by the same executable with its fonts and icons embedded.

## Features

- **Nine chatbox lines** — AFK, loading, time, text, memory, CPU, media, active window and
  VRChat logs. Each one can be switched on or off and dragged into any order.
- **Per-line settings** — every module has its own label, visibility and formatting options:
  decimal places for memory and CPU, which parts of the track to show, timer style, dot
  count, character limits and more.
- **Now playing from Windows** — reads the same media sessions the Windows volume flyout
  uses, so Spotify, Chrome, VLC and friends show up with title, artist, album, elapsed time
  and track length. An app picker pins the line to one player instead of following whichever
  session is current, and a keyword filter narrows it further.
- **Send a message right now** — type in the panel and push it to the chatbox for a set
  number of seconds, or pin it until you clear it.
- **Text rotation** — add, edit, delete and reorder messages, choose how long each stays up
  and optionally shuffle them.
- **AFK detection** — watches VRChat's outgoing OSC (movement and voice) plus foreground
  window changes, with a configurable threshold, label and timer format.
- **VRChat logs** — joins, leaves, an activity feed and a history of everything Fern sent,
  tailed incrementally instead of re-read every tick.
- **Settings that stick** — everything is written to `config.json` next to the executable,
  and old config files pick up new options with sensible defaults.
- **Self-contained UI** — no CDN, no web fonts, no framework; the stylesheet and fonts are
  embedded in the binary.

## Modules

| Module | Shows |
| --- | --- |
| AFK | Time since you were last active, or just a label |
| Loading | Growing dots (`.`, `..`, `...`) with optional text in front |
| Time | Clock, 12/24h, optional seconds and label |
| Text | Your rotating messages |
| Memory | RAM used, total and percentage |
| CPU | CPU name, core count and usage |
| Media | Track, artist, album, progress bar and `elapsed/total` |
| Active window | Title of the focused window |
| VRChat log | Last join or leave |

## Download

Grab `Fern.exe` from [Releases](https://github.com/MewishMC/Fern/releases) and run it.
Fern needs Windows 10 or 11 (x64) and the WebView2 runtime, which ships with Windows 11 and
current Windows 10 installs.

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

`config.json` is created next to the executable on first launch and rewritten whenever you
change a setting. Delete it to reset everything to defaults; unknown or missing fields in an
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
