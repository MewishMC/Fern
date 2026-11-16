# Changelog

Each release gets a section headed by its version tag, optionally followed by a name:

    ## v2.1 - Release name

The Release workflow publishes that section as the release notes and uses the name in the
release title, and Fern shows both in the panel before updating. Keep upcoming changes under
**Unreleased** and rename it to the version when you tag.

## Unreleased

## v2.4.1 - Avatar picker

### Added
- The Avatar page can show any avatar, not just the one you wear: search every avatar VRChat
  has an OSC config for, and change its name, profile and saved parameters.
- This avatar lists the avatar's parameters with their types, marks the read-only ones, and
  lets you change values: sent right away for the avatar you wear, or saved so they're put
  back when you change into it.
- Loading can type out your own text instead of the dots ("test" shows t, te, tes, test),
  with an optional blank fill such as the braille blank to keep the line the same width, a
  choice to start over or erase back, and a hold on the whole text.
- Lyrics can have a prefix and suffix around each lyric, like "🎤 " and " 🎶", on the lyrics
  line or the media line. The break marker gets them too.

### Changed
- Loading's "Show dots" is now "Animate", since it also turns the typed-out text on and off.
- The Update button keeps `update.exe` in `%LOCALAPPDATA%\Fern` with your settings instead of
  putting it next to `Fern.exe`. An `update.exe` you placed yourself still works as before.

### Fixed
- Avatars show their names instead of their avtr_ ID when VRChat's log is off or the avatar
  was worn before Fern started; Fern now reads the names from VRChat's OSC configs.

## v2.4 - Automation

### Added
- OSCQuery: Fern now listens for VRChat on a free port and tells VRChat where it is, so other
  OSC apps can keep port 9001. It is on by default and can be turned off on the Output page,
  which also shows its status. If OSCQuery can't start, or VRChat never finds Fern (for
  example when a firewall blocks it), Fern goes back to port 9001 by itself.
- Forward received OSC: pass what VRChat sends Fern on to up to eight other apps. Each target
  has its own switch and an optional address filter such as `/avatar/parameters/`, and shows
  how many packets it has passed on.
- Profiles: save the chatbox setup under a name (which lines show, their order and drop
  order, every line's settings and your messages) and switch between them on the new
  Profiles page, from the quick switcher on the Live page, from the tray icon's menu, or with
  a shortcut per profile that works in any app. While a profile is active, your changes are
  saved into it as you go. Profiles can be renamed, duplicated, reordered, overwritten and
  deleted, and can also carry the panel's look from the Appearance page.
- Profiles never hold API keys, tokens or sign-ins, the window and tray settings, or where the
  chatbox is sent; those stay as they are when you switch.
- Backup and restore on the App page: export every setting and profile to one file (without
  API keys, tokens or sign-ins) and import it again. Imported settings are checked before
  they replace anything.
- Fern keeps automatic backups in `%LOCALAPPDATA%\Fern\backups`: one before each update is
  installed and one before every import or restore, plus "Back up now". It keeps the last 10,
  and any of them can be restored from the App page.
- A first-run setup checks that VRChat's OSC is on (VRChat's own setting, its OSC folder and
  live OSC traffic), sends a test message, and turns on a starter set of lines from presets.
  Run it again any time from the App page. People updating from an earlier version are not
  asked.
- Rules: a new Rules page for "when this happens, do that". Triggers include joining a world,
  wearing an avatar, an avatar parameter, VR or desktop, a time of day on chosen weekdays,
  going AFK or coming back, the focused app, a player joining or leaving, Fern starting, a
  profile switch, a screenshot and a finished world load. Extra conditions narrow a rule
  down ("while in VR", "unless AFK"). Actions include switching profile, turning a line on or
  off, sending a chatbox message, setting an avatar parameter, using a message group, do not
  disturb and running another rule. Each rule has its own switch, an optional cooldown and a
  test button, and shows when it last fired. With "Undo when it ends", a rule for a world, a
  time window or AFK puts everything back once it stops holding.
- Do not disturb: one switch that hides every chatbox line except the ones you allow.
  Messages you send still show. Find it on the Live page, on the Lines page with its own
  shortcut, in the tray menu, and as a rule action.
- Message groups per world: "Use in this world" on the custom messages page and the Logs page
  adds a rule that cycles through that group whenever you're in the current world, and goes
  back to normal when you leave.
- Avatar page: everything Fern does with the avatar you wear, in one place, including a table
  of every parameter Fern reads and sends. Names are editable, groups switch on and off, and
  current values are shown live.
- Profiles per avatar: pick a profile for each avatar and Fern switches to it when you change
  into that avatar, with an optional profile for every other avatar. Recent avatars are
  remembered by name, can be renamed, and are suggested by the "Wearing an avatar" trigger.
- Saved parameters: tick an avatar's parameters (outfit toggles, colours) to save them now or
  keep them saved as they change. Fern puts them back when you change into that avatar, and
  can copy them to another avatar, such as a re-upload with a new ID.
- Media keys from your avatar: parameters for play/pause, next, previous, volume up and down,
  and mute (defaults like `FernMediaNext`), plus `FernMediaPlaying` sent back while music
  plays.
- VRChat events as avatar parameters: short pulses when someone joins or leaves, a world
  loads or you take a screenshot, plus AFK and the player count.
- Time and PC load as avatar parameters: hour, minute and second as ints and as 0-1 floats
  for clock hands (12- or 24-hour), and CPU, GPU and RAM load.
- Rules can press keys (tap, hold for a while, or hold until the rule ends), open or close
  apps, control media and put back saved avatar parameters. "Keys and apps from a parameter"
  on the Avatar page adds a ready-made rule in one click, e.g. hold Ctrl+M while a menu
  toggle is on.
- Voice commands: say a phrase into dictation to run a rule, for example switching a profile,
  setting an avatar parameter or sending a message. A command can match anywhere, the whole
  sentence, the start, or a regex. It can be push to talk only, and it can keep its words out
  of the chatbox. Find them on the new Voice page. Rules can also read text aloud and start
  dictation.
- Word filter: replace words or phrases (plain text or `/regex/`) and hide words behind a
  mask character, with separate switches for typed messages, dictation and read-aloud. Read
  aloud skips hidden words instead of spelling out the mask.
- Text to speech can now use an OpenAI-compatible server (OpenAI or a local one such as
  Kokoro), ElevenLabs or Azure Speech as well as the Windows voices. Voice lists are fetched
  from the service, and every engine plays through the same output device and can open your
  VRChat mic.
- Local Whisper presets for speech to text (whisper.cpp, faster-whisper-server, Speaches), a
  Test connection button, and an option to start your own whisper server when you dictate
  and close it with Fern. Fern doesn't include a model.
- Timer line: a countdown (for a set time or until a time of day), a stopwatch, or a Pomodoro
  timer with work, break and long-break phases, rounds and your own phase names. Start,
  pause, reset and skip it from the Live page, from global shortcuts, from an avatar
  parameter or from rules. Its template takes placeholders like "⏱ {label} {time}", and a
  message of your choice shows when a countdown runs out.
- The timer can send avatar parameters: one is on while it runs, and one goes from 0 to 1 as
  the countdown or phase gets closer to the end, for an animation on your avatar.
- Counters line: count headpats, boops or anything else an avatar parameter reports. A
  counter can count each time the parameter turns on or each time it changes, with an
  optional cooldown. Session and all-time totals are both kept and survive restarts. Add,
  take away or reset by hand from the Counters page or the Live page, give each counter a
  shortcut that adds one, and choose to show it only for a moment after it changes.
- Scheduled messages: send a message at a set time on chosen weekdays, or every few minutes
  within an optional time window. It shows for as long as you set, either on its own line or
  taking over the chatbox like Send now.
- Hardware sensors: Fern reads temperatures, power, clocks and fans from HWiNFO (with Shared
  Memory Support ticked) or LibreHardwareMonitor (its web server or WMI) while either is
  running. No driver is installed. The new Sensors page shows which app is answering, why
  the others aren't, and how to set each one up.
- A Sensors line that shows the sensors you pick, each with your own name, laid out by a
  template (`{name} {value}`, `{min}`, `{max}` and more).
- The CPU line can show temperature, package power, clock and fan, and the GPU line can show
  power, clock and fan. Fern picks the right sensors itself, or you choose them. GPU
  temperature now works for AMD and Intel cards when HWiNFO or LibreHardwareMonitor is
  running.
- An Uptime line: how long the PC has been on and how long Fern has been running.
- A Ping line: the round trip to a host you choose, with the average and packet loss. If the
  host ignores normal pings, Fern times a TCP connection instead.
- A Disk usage line for the drives you pick, showing used, size, free and percent.
- A PC battery line for laptops, with a charging mark, a low icon and the time left.
- Calendar line: your next event from iCal links (Google, Outlook, webcal://) or local .ics
  files, like "Next: Stand-up in 25m". It understands all-day events, time zones and
  repeating events, can show the event you're in ("Now: Meeting, 20m left"), and hides
  itself when nothing starts within the time you pick. Links are stored encrypted, since
  they work without a password.
- Prices line: exchange rates (ECB rates through Frankfurter) and crypto prices (CoinGecko)
  for up to 8 pairs, like "BTC $67,123 ▲2.1% · USD €0.9213 ▼0.3%". No account needed. Fern
  slows down by itself when a service asks it to.
- Steam line: the game Steam is running and how long you've been playing it, like
  "🎮 Portal 2 · 1h 05m", read straight from Steam on your PC. Add a Steam Web API key to get
  your total playtime too. VRChat and SteamVR are left out by default.
- Controller haptics: a new Controllers page makes your SteamVR controllers buzz on every
  heartbeat, when someone joins your instance, or when an avatar parameter changes. Each one
  has its own controller, strength, length and number of pulses, and a button to try it.
- Controller input as avatar parameters: thumbsticks, trackpads, trigger, grip and (with
  SteamVR Input) finger curl are sent to your avatar. Fern also sends where your thumb rests
  and which way the stick or trackpad points, in four or eight directions. Thumb numbering
  matches ThumbParams, so avatars made for it work.
- Twitch chat line: shows your newest chat message, with filters for commands, bots, links,
  chosen users and words, and an option to show only subs, VIPs or mods. Without a sign-in
  it reads chat anonymously.
- Twitch alerts for follows, subs, gift subs, raids, cheers and channel point redeems. They
  appear on the Twitch chat line one at a time, each with its own text, and leaving a text
  empty turns that alert off. Subs, gifts, raids and cheers work on any channel; follows and
  redeems need the sign-in on your own channel.
- TikTok Live line, read from TikFinity or a TikTok-Live-Connector server you run: viewers,
  likes and new followers, plus alerts for follows, gifts, shares and follower milestones.
- Kick line with live status, viewers and followers from Kick's public channel page.
- YouTube line with live status, viewers and subscribers, using your own YouTube Data API
  key (stored encrypted).
- Spotify, optional: sign in with your own Spotify app (no client secret needed) to show
  whether the song is in your Liked Songs, shuffle and repeat, the device and the playlist
  on the media line. Each one can be turned on separately, and they only show while Spotify
  plays the same song Windows reports.
- Spotify controls on the Media page: like or unlike the song, shuffle, repeat, volume, move
  playback to another device, and add a song to the queue by link or by searching. Controls
  need Spotify Premium.
- Spotify avatar parameters: your avatar can like songs, toggle shuffle and repeat, set the
  volume from a radial, switch devices and queue a chosen song. Fern sends the real state
  back so menu toggles match, and ignores the values VRChat sends while an avatar loads.
- Last.fm line: shows what you are scrobbling from any player or device, with your play
  count, a loved mark and your total scrobbles if you want them, or your own layout. Your
  username and API key are stored encrypted.
- OBS line: shows when you're live or recording in OBS Studio, with the running time (and a
  paused recording), plus the current scene if you want it. Connects to OBS's built-in
  WebSocket server (OBS 28 or later); the server password is stored encrypted.
- OBS page: start and stop the stream or the recording, pause the recording, switch scenes,
  and show or hide the current scene's sources from Fern.
- Chatbox overlay for OBS: add a Browser source pointing at Fern's overlay address and it
  shows exactly what your chatbox says, live, on a transparent background. Style it from the
  OBS page (size, colours, bubble, alignment, outline), with more options in the address, or
  with OBS's Custom CSS.
- Voicemod mute: a Mute/Unmute button, a global shortcut and a rules action (with undo). The
  Voicemod line can say when you're muted.
- Voicemod hold-to-bleep: hold the panel button or a global shortcut to cover your voice
  with Voicemod's beep, or bleep for a few seconds from a rule. A bleep always stops by
  itself after the longest bleep you set.
- **Control API**: turn it on on the new API page to drive Fern from a Stream Deck, Touch
  Portal, Streamer.bot, PowerShell or curl. It can send a message, set or clear a custom
  line, switch lines, profiles, do not disturb or the whole chatbox, run a rule or a single
  rule action, and read what Fern is showing. It works over HTTP or a WebSocket that pushes
  changes. It only answers programs on this PC that send the token shown on the page; web
  pages are always refused. The page has copy-paste examples for each tool.
- **Custom lines**: three new chatbox lines whose text comes from the API or a rule, from a
  command or script you choose (run every few seconds with no console window, first line of
  output), or from a web address, with an optional JSON field such as `data.viewers`. Each
  has a label, length limit and text to show when there's nothing else, and works with line
  order, drop order and VR/desktop visibility like any other line.
- **Clipboard to chatbox**: a global shortcut (App page) that sends whatever you copied
  straight to the chatbox, with a length limit, a choice to cut or skip text that is too
  long, and an optional read-out.
- Chatbox motion: give any line a scroll, typewriter or blink effect on the Lines page.
  Scroll shows a moving window of a long line, typewriter types a line out when it changes
  (only the part that changed), and blink shows a line every other step. Steps never go
  faster than VRChat's chatbox limit, and the preview shows exactly what VRChat gets.
- Chatbox pages: group lines into named pages that take turns, each shown for its own time.
  Lines in no page show on every page, and empty pages are skipped.
- Blocked worlds: like VRCOSC, Fern now pauses the chatbox in club and event worlds on the
  community blocklist, which it updates once a day. You can add your own worlds, unblock
  community ones, choose which lines still show (heart rate by default), and use "Show
  anyway" to keep the chatbox on in the current world until Fern closes. Messages you send
  yourself always show. On by default; turn it off on the Lines page.
- An emoji picker in Send now and the custom message editor, with search, categories, plain
  text symbols and kaomoji. It remembers what you used last, and right-click stars a
  favourite. Everything is bundled, so it works offline.
- Save this session from the Logs page: messages you sent, everything the chatbox showed, or
  joins, leaves and worlds, as a .txt file or a .csv spreadsheet.
- New rule triggers: timer finished, Pomodoro phase changed, timer running, counter went up
  (optionally only every Nth time), scheduled message sent, CPU or GPU temperature, any
  hardware sensor, on battery, slow ping, controller input (a thumbstick or trackpad
  pointing a chosen way, or your thumb resting on a button), Twitch chat messages, follows,
  subs, gift subs, raids, cheers, channel point redeems and going live, TikTok chat,
  follows, gifts, likes, shares, viewers and follower milestones, "Spotify song changes",
  "Song is in Liked Songs", "Spotify plays on a device", "Last.fm now scrobbling" (optionally
  only loved songs), OBS going live, stopping or changing scene, muted in Voicemod, Control
  API call, chatbox paused in a blocked world, chatbox page shown, a calendar event
  happening or starting soon, a price going above or below a value, and a Steam game
  running, starting or closing.
- New rule actions: control the timer, change a counter, show a scheduled message, buzz the
  controllers, send a Twitch announcement, give a Twitch shoutout, Spotify like, shuffle,
  repeat, volume, play on a device and add to the queue, start, stop or toggle the OBS
  stream and recording, switch the OBS scene, show or hide an OBS source, set a custom line,
  show a chatbox page, and show the chatbox anyway in a blocked world. Actions that change
  something undo where it makes sense, and rules that use OBS connect by themselves.

### Changed
- Fern receives VRChat's OSC through one shared listener. AFK detection works as before.
- Heart rate avatar parameters now go to the extra outputs too, not only the main one, the
  same as the chatbox.
- The heart rate source now also connects while heartbeat haptics are on, even with the
  heart rate line off.
- Profile switches leave rules and the do not disturb switch alone, like the Display switch.
- Sign in with Twitch asks for more permissions, for chat, alerts, announcements and
  shoutouts. A sign-in made before this update keeps working for the Twitch line, and the
  Twitch pages ask you to sign in again to turn the new parts on.
- Custom lines, the control API and the clipboard shortcut stay on this PC: profiles don't
  switch them. Importing a backup never brings in a custom line's command or the whisper
  server Fern starts for dictation; this PC's own stay in place.

### Fixed
- Two Twitch connections renewing the sign-in at the same moment can no longer sign you out.

## v2.3.1 - Plain lyrics

### Changed
- Tracks that only have plain lyrics now show them anyway: Fern spreads the lines over the
  track's length and follows the playback position. Plain .lrc files without timestamps work
  the same way. Use the lyrics offset if the lines run early or late.

### Fixed
- The "plain" text style could turn a small-caps "ᴋ" into "+".

## v2.3 - All the lines

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
- AFK detection can count keyboard and mouse use in any app, next to VRChat movement, voice,
  other OSC and window switching. Pick which ones count; keyboard and mouse can be limited to
  desktop. Each source shows when it last saw you.
- An **I'm AFK** switch on the AFK page and an **I'm AFK** button on Live keep you AFK until
  you turn it off.
- 14 AFK styles with live previews, a separate text for when the timer is off, an optional
  icon (💤 by default), `{time}` and `{since}` placeholders, two new timer formats ("1h 12m",
  "12 minutes") and Unicode lettering for the timer.
- An **Aa** button on the AFK texts and the text label restyles them in small caps,
  superscript, bold, italic, monospace, double struck or fullwidth.
- Custom messages have a heart to take them in or out of the rotation and an optional group.
  The rotation can be limited to one group.
- A weighted shuffle that brings back the messages you haven't seen for longest.
- A list of icons put in front of custom messages, the next one (or a random one) each time
  the message changes.
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
- Each line can show always, only in VR or only on desktop.
- A prefix and suffix can be put around every chatbox line.
- The chatbox can go to up to two more OSC outputs, each with its own host, port and switch.
- Send now has live typing (sends as you type, kept to VRChat's rate), shows VRChat's typing
  bubble while you write, remembers your last 20 messages to resend with a click, and
  suggests a whole earlier message as you type it (Tab takes it).
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
- Custom messages are saved as objects with a heart and a group. Existing message lists are
  converted when loaded; older Fern versions cannot read the new format.
- Media titles from video sites are tidied: "(Official Video)", "[HD]", "| Official Music
  Video", repeated "ft." credits and "Artist - " prefixes are dropped. Turn off **Tidy
  titles** on the Media page to keep them.
- A chatbox that is too long now fits instead of being cut off mid-line: media first loses its
  bar, its time, then album and feat. credits, then whole lines are dropped by a priority you
  set on the Lines page (time first, your text last by default), and only the last line left
  is cut with "…". The preview and the Lines page say which lines were dropped or shortened.
- VRChat's chatbox sound can play for sent messages (on by default, once per message) and,
  separately, for automatic updates.

### Fixed
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
