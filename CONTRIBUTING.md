# Contributing to Fern

Thanks for taking a look. Bug reports, fixes and new chatbox lines are all welcome.

## Reporting a bug

Open an issue with:

- what you expected and what happened instead,
- your Windows version and the Fern version shown in the panel,
- the relevant part of `config.json` if a setting is involved (remove anything personal),
- any error text, or the output of running Fern from a terminal so the logs are visible.

## Setting up

You need Go 1.23+, MinGW-w64 (`gcc` and `windres` on `PATH`) and the WebView2 runtime.

```bat
run.bat
```

`run.bat` is just `go run ./Fern`. For a release-style build with icon and manifest, run
`build.bat`. Start Fern with `--debug` to get WebView developer tools.

## Before opening a pull request

```bat
gofmt -l .
go vet ./...
go build ./...
```

`go vet` and `go build` compile the WebView package, so they need a working C toolchain.
Please run the app and exercise the module you touched; most of Fern is visual, and the UI
is plain HTML/CSS/JS with no test harness.

## Code layout

- `Fern/modules/` holds one file per chatbox line. Each line is rendered by a function that
  takes the current `Config`, so the same code drives the preview and the real chatbox.
- `Fern/modules/config.go` owns every option: the struct, its defaults in `DefaultConfig`,
  and its bounds in `sanitize`. A new option needs all three, otherwise old config files
  either lose it or accept nonsense values.
- `Fern/UI/home.html` is the panel. It receives a single state object over the websocket and
  sends `{type: "config"}` messages back; keep the page free of external requests, since the
  assets are embedded and the app is expected to work offline.
- `Fern/server.go` serves the UI and translates panel actions into module calls.

## Style

- Run `gofmt` before committing.
- Keep the UI dependency-free: no CDN links, no frameworks, no remote fonts.
- Prefer extending an existing module over adding a parallel code path.
- Write commit subjects that say what changed; a short body explaining why is welcome.

## Releases

The bundled updater reads `https://github.com/MewishMC/Fern/releases/latest` and expects an
asset named `Fern.exe` on that release. To publish a version, run `build.bat` and attach the
resulting `Fern.exe` to a new release.

## License

By contributing you agree that your changes are licensed under the repository's
[MIT License](LICENSE).
