# Changelog

Each release gets a section headed by its version tag, optionally followed by a name:

    ## v2.1 - Release name

The Release workflow publishes that section as the release notes and uses the name in the
release title, and Fern shows both in the panel before updating. Keep upcoming changes under
**Unreleased** and rename it to the version when you tag.

## Unreleased

### Added
- The window line can rename apps, show an app's window title or both, or hide it, per app.
  Recently focused apps can be picked from a list, "Only show listed apps" hides everything
  else, and hidden apps can send a replacement text such as "busy".
- Windows whose title contains a word or matches a /regex/ can be hidden. Incognito and
  InPrivate windows are hidden by default.
- The window line can start with "In VR" or "On desktop", detected from SteamVR (and
  optionally Oculus), with your own wording.
- Apps without a custom name show their real product name (from the executable) instead of
  the window title.
- A Network line with download and upload speed, the session peak and totals, and how busy
  the connection is against the link speed or the fastest rate seen. Units pick themselves
  (kbps, Mbps, Gbps or bytes), small raised labels save characters, and the adapter can be
  picked or left on automatic (the busiest one).
- A GPU line with load and video memory for any graphics card, read from the counters Task
  Manager uses, plus the temperature on NVIDIA cards. The card can be picked or left on
  automatic.
- The CPU and GPU lines can show a custom name instead of the detected model.
- A VRChat world line with the world name, player count and peak, instance type, region,
  time in the instance and session stats.
- The VRChat log line can announce screenshots and finished world loads ("Loaded The Black
  Cat in 14s · 230 MB"), marks players you already met this session ("joined again") and can
  hide events after a set time.
- The Logs page shows this session: the current world and instance, players here and the
  peak, worlds visited and players met.
- An App page with an optional tray icon (click to show, right-click to quit), start
  minimised, close to tray and minimise to tray.
- A global shortcut that toggles your VRChat mic over OSC.
- Fern can register itself with SteamVR to start minimised with it and quit when SteamVR
  closes. This only happens when you press Register while SteamVR is running.

### Changed
- VRChat log events use the time from the log line, and "OnPlayerLeftRoom" lines no longer
  show up as a player called "Room" leaving.

## v2.2 - Custom bars

### Added
- The music progress bar can be restyled: pick a preset (blocks, line, shade, squares, dots,
  classic) or set your own played and unplayed characters, a position marker and end caps.

### Changed
- Neutral colours are on by default.
- Settings are saved in `%LOCALAPPDATA%\Fern\config.json` instead of next to Fern.exe. An
  existing `config.json` next to Fern is moved there on first launch and the original kept
  as `config.json.old`.

## v2.1 - Modules and updates

### Added
- A Modules page lists every chatbox line with search, on/off filters, a live preview of
  what each one sends and a shortcut to its settings.
- Fern checks for new releases and offers an **Update** button that shows the release's
  name and changelog, then installs it and restarts.
- An **Output** page for the OSC host, port and update rate.
- Undo after removing a text message.

### Changed
- Settings that have no effect with the current options are dimmed.
- Media's playback switch and progress style are one control; AFK detection is one switch.
- Fern reopens the page that was open last.
- The chatbox always uses VRChat's limits of 144 characters and 9 lines.

### Fixed
- Actions no longer report success while the panel is disconnected.
- Typing an OSC port no longer sends to every partial number along the way.
- The updater starts Fern even when it can't reach GitHub or there is no release yet.

### Removed
- The Save now button; settings save as you change them.
- The max characters and max lines options.
