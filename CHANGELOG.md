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
- The media line can start with your own words per state ("Listening to", "Paused"), show
  stopped players with their own icon, use any artist separator (such as " ᵇʸ "), go
  uppercase and write the playback time in small raised digits.
- "Only show on change" shows the media line for a while (25 seconds by default) after the
  track changes or you pause or resume, then hides it.
- Progress bar styles can be saved under a name, and shared as text with Export and Import.
- A Lyrics line shows the line being sung, synced to the music, from LRCLIB or your own
  `.lrc` files. It can replace the track on the media line, with adjustable timing, hold
  time and a marker for instrumental breaks. Each track is looked up once per run.
- The time line can use any time zone, show its abbreviation, add a second clock and count
  the time since or until dates, like "together 1y 2m".
- A Weather line shows the conditions, temperature, feels-like, humidity and wind for a city
  or coordinates you choose, from Open-Meteo, in °C or °F and km/h, mph or m/s. It can sit
  on the time line.

### Changed
- Media titles from video sites are tidied: "(Official Video)", "[HD]", "| Official Music
  Video", repeated "ft." credits and "Artist - " prefixes are dropped. Turn off **Tidy
  titles** on the Media page to keep them.

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
