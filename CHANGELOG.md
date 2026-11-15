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
- Appearance has a font picker: search the fonts installed on your PC (each shown in its own
  face) or upload a .ttf, .otf, .woff or .woff2 file, with a live preview. Uploaded fonts are
  kept in `%LOCALAPPDATA%\Fern\fonts` and included in appearance exports.
- Tracker battery line: the charge of your headset (when it reports one), controllers and
  trackers from SteamVR, with your own per-device template, icons, a charging mark, a
  low-battery tag and threshold, "only low" and "only the lowest" modes, sorting, and custom
  names or hiding per device, remembered by serial number.
- VR performance line: frame rate, headset refresh rate, reprojection, dropped frames, GPU
  and CPU frame time, frame budget and motion smoothing from the SteamVR compositor. It can
  stay hidden until performance drops below your thresholds, and only clears after running
  smoothly for a set time so it does not flicker.
- Fern stays a single exe: Valve's OpenVR library is built in and only unpacked to
  %LOCALAPPDATA%\Fern\openvr when SteamVR is running and one of these lines is on. Fern
  connects as a background app, so it never starts SteamVR and lets go when SteamVR quits.
- Heart rate line from Pulsoid (paste your token), HypeRate (your own API key and session ID)
  or any websocket or HTTP URL that returns JSON, with a JSON field path. It can show a heart
  that changes with your zone, a bpm suffix, a ↑ ↓ → trend arrow with adjustable sensitivity,
  smoothing, an offset, low/high words such as "sleepy" and "hot", the session min/avg/max,
  and an offline text or hide itself when no data has arrived for a while.
- Heart rate can drive avatar parameters: HR (int), HeartRateFloat (−1…1 or 0…1), a beat
  toggle or pulse, and a connected flag, all with editable names.
- The heart rate page shows the connection status, the live bpm and a five-minute sparkline.
  Tokens and keys are masked and never appear in logs or messages.
- AI tools under Send now: fix spelling and grammar, shorten to fit, rewrite in a style,
  translate, suggest a reply or a conversation starter. Works with any OpenAI-compatible
  server (OpenAI, Ollama, LM Studio and others) or Anthropic, with your own key, which is
  stored encrypted for your Windows account. Results are shown for you to edit and accept
  before anything is sent, with the tokens each request used.
- Text to speech with the Windows voices, no download needed: pick a voice, speed, volume
  and output device (such as a virtual cable), read out sent messages, cut off the previous
  one, and optionally open your VRChat mic over OSC while it speaks.
- Dictation: hold or click the mic button next to Send now, with a microphone picker, noise
  gate with a live level meter and silence timeout. Speech is transcribed by an
  OpenAI-compatible endpoint, can be translated, and can be sent straight away.

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
