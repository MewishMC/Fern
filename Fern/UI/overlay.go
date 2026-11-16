package UI

import _ "embed"

// OverlayPage is the chatbox overlay for an OBS browser source, served at
// /overlay. It follows the chatbox text through /overlay/stream.
//
//go:embed overlay.html
var OverlayPage string
