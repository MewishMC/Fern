package modules

import "slices"

// Do not disturb hides every chatbox line except the ones on its allow
// list, without touching which lines are switched on. Messages sent from
// the Live page (and by rules) still show.

// DNDOptions is the do-not-disturb switch. It is kept per PC, like the
// Display switch, so a profile switch never turns it on or off.
type DNDOptions struct {
	On     bool     `json:"on"`
	Allow  []string `json:"allow"`  // line ids that still show while it is on
	Hotkey string   `json:"hotkey"` // global shortcut that toggles it; "" is none
}

func defaultDNDOptions() DNDOptions {
	return DNDOptions{Allow: []string{}}
}

func (o DNDOptions) clone() DNDOptions {
	o.Allow = append([]string{}, o.Allow...)
	return o
}

func (o *DNDOptions) sanitize() {
	allow := make([]string, 0, len(o.Allow))
	for _, id := range o.Allow {
		if knownModules[id] && !slices.Contains(allow, id) {
			allow = append(allow, id)
		}
	}
	o.Allow = allow
	if o.Hotkey != "" {
		if _, _, canonical, err := ParseHotkey(o.Hotkey); err == nil {
			o.Hotkey = canonical
		} else {
			o.Hotkey = ""
		}
	}
}

// dndHides reports whether do not disturb keeps line id out of the chatbox.
func dndHides(o DNDOptions, id string) bool {
	return o.On && !slices.Contains(o.Allow, id)
}

// dndQuieted lists the enabled lines do not disturb is hiding, in order.
func dndQuieted(cfg Config) []string {
	if !cfg.DND.On {
		return nil
	}
	var out []string
	for _, id := range cfg.DisplayOptions.Order {
		if ModuleEnabled(id, cfg.DisplayOptions) && dndHides(cfg.DND, id) {
			out = append(out, id)
		}
	}
	return out
}

// DNDActive reports whether do not disturb is on.
func DNDActive() bool {
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	return cfg.DND.On
}

// SetDND turns do not disturb on or off. It reports whether that changed
// anything.
func SetDND(on bool) bool {
	if DNDActive() == on {
		return false
	}
	UpdateConfig(func(c *Config) { c.DND.On = on })
	NudgeRules()
	return true
}

// ToggleDND flips do not disturb and returns the new state.
func ToggleDND() bool {
	on := false
	UpdateConfig(func(c *Config) {
		c.DND.On = !c.DND.On
		on = c.DND.On
	})
	NudgeRules()
	return on
}
