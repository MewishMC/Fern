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
- Discord: Rich Presence shows your VRChat world, instance type, player count and time in
  the world on your profile, with an optional Join button for public instances and a button
  of your own. Uses your own Discord application ID, set up from the new Discord page.
- Discord voice line: your voice channel, how many are in it, who is talking and your mute
  or deafen state, with layout presets, a limit on listed names and an option to leave
  yourself out. Needs your application's client secret and a one-time approval in Discord.
- Twitch line: live or offline, viewers, game, title and followers with short counts
  (1.2k), or your own layout. Sign in with Twitch from the panel using your app's client ID.
- Soundpad line: the sound you just played in Soundpad, shown for as long as you choose.
- Voicemod: switch voices and play sounds from the panel, and show the sound and the
  active voice in the chatbox. Needs a Voicemod Control API client key.
- Tokens, client secrets and keys for these are stored encrypted for your Windows account
  in `secrets.dat`, never shown again in the panel and never written to `config.json`.
  Each integration has a status badge and stays quiet when its app is not running.

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
