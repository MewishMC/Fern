package main

import (
	"encoding/json"
	"net/http"

	"Fern/modules"
)

// textStyleScript is the panel's copy of the unicode text styler. The tables
// come from modules.TextStyleTables, so the page and the chatbox can never
// disagree about what a style looks like. Any page can load it with
// <script src="/textstyle.js"> and call FernTextStyle.apply(text, style).
var textStyleScript = func() []byte {
	tables, _ := json.Marshal(modules.TextStyleTables())
	styles, _ := json.Marshal(modules.TextStyles)
	return []byte(`"use strict";
window.FernTextStyle = (function () {
    var tables = ` + string(tables) + `;
    var styles = ` + string(styles) + `;
    // Placeholders in braces, like {time}, are left alone, as in StyleText.
    function apply(text, style) {
        var table = tables[style];
        if (!table || !text) return text || "";
        var out = "", depth = 0;
        Array.from(text).forEach(function (ch) {
            if (ch === "{") depth++;
            else if (ch === "}" && depth > 0) depth--;
            else if (depth === 0 && Object.prototype.hasOwnProperty.call(table, ch)) { out += table[ch]; return; }
            out += ch;
        });
        return out;
    }
    return { styles: styles, apply: apply };
})();
`)
}()

func serveTextStyleJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(textStyleScript)
}
