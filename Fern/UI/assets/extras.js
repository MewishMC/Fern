/* The emoji picker, the session export and the first-run setup.
   Loaded after the panel's main script and built on its helpers ($, send,
   toast, pushConfig, config, S, MODULE_NAME); applyState calls
   FernExtras.render(st) with every state push. */
(function () {
    "use strict";

    const data = window.FernEmojiData || { emoji: [], symbols: [], kaomoji: [] };
    const RECENT_MAX = 30;
    const FAVORITE_MAX = 60;

    function el(tag, className, text) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (text !== undefined) node.textContent = text;
        return node;
    }

    function chatOptions() {
        const c = config();
        return (c && c.ChatOptions) || {};
    }

    /* ================= emoji picker ================= */

    // Every item once, for search and for naming recents: { c, w, kind }.
    const ALL = [];
    const NAMES = new Map();
    data.emoji.forEach((cat) => cat.items.forEach((it) => ALL.push({ c: it.c, w: it.w, kind: "emoji" })));
    data.symbols.forEach((g) => g.items.forEach((it) => ALL.push({ c: it.c, w: it.w, kind: "symbol" })));
    data.kaomoji.forEach((g) => g.items.forEach((it) => ALL.push({ c: it.c, w: it.w, kind: "kaomoji" })));
    ALL.forEach((it) => { if (!NAMES.has(it.c)) NAMES.set(it.c, it.w); });

    // searchItems keeps items whose words contain every word typed, or that
    // are exactly what was typed (pasting ♡ finds ♡).
    function searchItems(query) {
        const words = query.toLowerCase().split(/\s+/).filter(Boolean);
        if (!words.length) return [];
        const seen = new Set();
        return ALL.filter((it) => {
            if (seen.has(it.c)) return false;
            const hit = it.c === query.trim() || words.every((w) => it.w.includes(w));
            if (hit) seen.add(it.c);
            return hit;
        });
    }

    const P = {
        root: null,
        target: null,   // the input or textarea being typed into
        msgIndex: -1,   // for the custom messages: which row, since the list repaints
        mode: "saved",
        hovered: "",
        built: {}
    };

    function buildPicker() {
        const root = el("div", "emoji-picker");
        root.id = "emojiPicker";
        root.hidden = true;
        root.setAttribute("role", "dialog");
        root.setAttribute("aria-label", "Emoji, symbols and kaomoji");

        const head = el("div", "ep-head");
        const search = el("input", "ep-search");
        search.type = "text";
        search.placeholder = "Search emoji, symbols, kaomoji";
        search.setAttribute("aria-label", "Search emoji, symbols and kaomoji");
        search.autocomplete = "off";
        const close = el("button", "mini-x ep-close", "✕");
        close.type = "button";
        close.title = "Close";
        close.setAttribute("aria-label", "Close the picker");
        head.append(search, close);

        const modes = el("div", "ep-modes");
        modes.setAttribute("role", "tablist");
        [["saved", "fa-star", "Saved"], ["emoji", "fa-face-smile", "Emoji"], ["symbols", "fa-icons", "Symbols"], ["kaomoji", "fa-font", "Kaomoji"]].forEach(([id, icon, label]) => {
            const b = el("button", "ep-mode");
            b.type = "button";
            b.dataset.mode = id;
            b.setAttribute("role", "tab");
            b.innerHTML = '<i class="fa ' + icon + '" aria-hidden="true"></i>';
            b.appendChild(el("span", "", label));
            modes.appendChild(b);
        });

        const cats = el("div", "ep-cats");
        data.emoji.forEach((cat) => {
            const b = el("button", "ep-cat");
            b.type = "button";
            b.dataset.cat = cat.id;
            b.title = cat.name;
            b.setAttribute("aria-label", cat.name);
            b.innerHTML = '<i class="fa ' + cat.icon + '" aria-hidden="true"></i>';
            cats.appendChild(b);
        });

        const body = el("div", "ep-body");
        body.tabIndex = -1;

        const foot = el("div", "ep-foot");
        const name = el("span", "ep-name", "Click to insert · right-click to star");
        const star = el("button", "btn ghost small ep-star");
        star.type = "button";
        star.disabled = true;
        star.innerHTML = '<i class="fa fa-star" aria-hidden="true"></i> Star';
        foot.append(name, star);

        root.append(head, modes, cats, body, foot);
        document.body.appendChild(root);

        P.root = root;
        P.search = search;
        P.body = body;
        P.cats = cats;
        P.name = name;
        P.star = star;

        close.addEventListener("click", () => closePicker(true));
        search.addEventListener("input", () => paint());
        search.addEventListener("keydown", (ev) => {
            if (ev.key === "Enter") {
                const first = body.querySelector(".ep-item");
                if (first) insert(first.dataset.c);
                ev.preventDefault();
            }
        });
        modes.addEventListener("click", (ev) => {
            const b = ev.target.closest("[data-mode]");
            if (!b) return;
            P.mode = b.dataset.mode;
            search.value = "";
            paint();
        });
        cats.addEventListener("click", (ev) => {
            const b = ev.target.closest("[data-cat]");
            if (!b) return;
            const section = body.querySelector('[data-section="' + b.dataset.cat + '"]');
            if (section) body.scrollTop = section.offsetTop - body.offsetTop;
        });
        // Keep the caret where it is: a click on an item must not take focus
        // from the box being typed in.
        body.addEventListener("mousedown", (ev) => {
            if (ev.target.closest(".ep-item")) ev.preventDefault();
        });
        body.addEventListener("click", (ev) => {
            const item = ev.target.closest(".ep-item");
            if (item) insert(item.dataset.c);
        });
        body.addEventListener("contextmenu", (ev) => {
            const item = ev.target.closest(".ep-item");
            if (!item) return;
            ev.preventDefault();
            toggleFavorite(item.dataset.c);
        });
        const describe = (ev) => {
            const item = ev.target.closest && ev.target.closest(".ep-item");
            if (!item) return;
            P.hovered = item.dataset.c;
            showName();
        };
        body.addEventListener("mouseover", describe);
        body.addEventListener("focusin", describe);
        star.addEventListener("mousedown", (ev) => ev.preventDefault());
        star.addEventListener("click", () => { if (P.hovered) toggleFavorite(P.hovered); });
        document.addEventListener("keydown", (ev) => {
            if (ev.key === "Escape" && !root.hidden) {
                ev.stopPropagation();
                closePicker(true);
            }
        });
        document.addEventListener("mousedown", (ev) => {
            if (P.root.hidden) return;
            if (P.root.contains(ev.target) || ev.target.closest("[data-emoji-for]")) return;
            closePicker(false);
        });
        window.addEventListener("resize", () => { if (!P.root.hidden) closePicker(false); });
    }

    function showName() {
        const favs = chatOptions().emoji_favorites || [];
        const on = favs.includes(P.hovered);
        P.name.textContent = P.hovered ? P.hovered + "  " + (NAMES.get(P.hovered) || "") : "Click to insert · right-click to star";
        P.star.disabled = !P.hovered;
        P.star.innerHTML = '<i class="fa fa-star" aria-hidden="true"></i> ' + (on ? "Unstar" : "Star");
        P.star.classList.toggle("on", on);
    }

    function itemButton(c, starred) {
        const b = el("button", "ep-item" + (c.length > 4 && !/\p{Extended_Pictographic}/u.test(c) ? " wide" : "") + (starred ? " starred" : ""), c);
        b.type = "button";
        b.dataset.c = c;
        const words = NAMES.get(c);
        b.title = words ? words : c;
        b.setAttribute("aria-label", words ? c + " " + words : c);
        return b;
    }

    function section(id, title, items, favs, empty) {
        const wrap = el("div", "ep-section");
        if (id) wrap.dataset.section = id;
        wrap.appendChild(el("div", "ep-title", title));
        const grid = el("div", "ep-grid");
        items.forEach((c) => grid.appendChild(itemButton(c, favs.has(c))));
        if (!items.length && empty) grid.appendChild(el("div", "ep-empty", empty));
        wrap.appendChild(grid);
        return wrap;
    }

    function paint() {
        const query = P.search.value.trim();
        const opts = chatOptions();
        const favs = new Set(opts.emoji_favorites || []);
        P.root.querySelectorAll(".ep-mode").forEach((b) => {
            const on = !query && b.dataset.mode === P.mode;
            b.classList.toggle("on", on);
            b.setAttribute("aria-selected", on ? "true" : "false");
        });
        P.cats.hidden = !!query || P.mode !== "emoji";
        const body = P.body;
        body.innerHTML = "";
        const frag = document.createDocumentFragment();
        if (query) {
            const hits = searchItems(query);
            const groups = [["emoji", "Emoji"], ["symbol", "Symbols"], ["kaomoji", "Kaomoji"]];
            groups.forEach(([kind, title]) => {
                const list = hits.filter((h) => h.kind === kind).slice(0, 160).map((h) => h.c);
                if (list.length) frag.appendChild(section("", title, list, favs));
            });
            if (!hits.length) frag.appendChild(el("div", "ep-empty", "Nothing matches “" + query + "”"));
        } else if (P.mode === "saved") {
            frag.appendChild(section("", "Starred", opts.emoji_favorites || [], favs, "Right-click anything to star it."));
            frag.appendChild(section("", "Recent", opts.emoji_recent || [], favs, "What you insert shows up here."));
        } else if (P.mode === "emoji") {
            data.emoji.forEach((cat) => frag.appendChild(section(cat.id, cat.name, cat.items.map((i) => i.c), favs)));
        } else {
            const groups = P.mode === "symbols" ? data.symbols : data.kaomoji;
            groups.forEach((g) => frag.appendChild(section("", g.name, g.items.map((i) => i.c), favs)));
        }
        body.appendChild(frag);
        body.scrollTop = 0;
        showName();
    }

    // The box a picker button types into. data-emoji-for="msgList" means the
    // custom message last typed in.
    function resolveTarget(key) {
        if (key === "msgList") {
            const inputs = document.querySelectorAll("#msgList .msg-input");
            if (!inputs.length) return null;
            const pick = document.querySelector('#msgList .msg-input[data-msg="' + P.msgIndex + '"]');
            return pick || inputs[inputs.length - 1];
        }
        return document.getElementById(key);
    }

    function openPicker(button) {
        if (!P.root) buildPicker();
        const key = button.dataset.emojiFor;
        const target = resolveTarget(key);
        if (!target) {
            toast(key === "msgList" ? "Add a message first" : "Nothing to type into", true);
            return;
        }
        if (!P.root.hidden && P.target === target) {
            closePicker(true);
            return;
        }
        P.target = target;
        P.key = key;
        P.mode = (chatOptions().emoji_recent || []).length || (chatOptions().emoji_favorites || []).length ? "saved" : "emoji";
        P.search.value = "";
        P.hovered = "";
        P.root.hidden = false;
        paint();
        place(button);
        target.focus({ preventScroll: true });
    }

    function place(button) {
        const r = button.getBoundingClientRect();
        const w = P.root.offsetWidth;
        const h = P.root.offsetHeight;
        // Open towards the side with room: from the button's left edge, or
        // ending at its right edge when it sits near the right of the window.
        let left = r.left + w <= window.innerWidth - 8 ? r.left : r.right - w;
        left = Math.min(left, window.innerWidth - w - 8);
        let top = r.bottom + 6;
        if (top + h > window.innerHeight - 8) top = Math.max(8, r.top - h - 6);
        P.root.style.left = Math.max(8, left) + "px";
        P.root.style.top = top + "px";
    }

    function closePicker(refocus) {
        if (!P.root || P.root.hidden) return;
        P.root.hidden = true;
        if (refocus && P.target && P.target.isConnected) P.target.focus({ preventScroll: true });
    }

    function insert(text) {
        let target = P.target;
        if (!target || !target.isConnected) target = P.key ? resolveTarget(P.key) : null;
        if (!target) {
            closePicker(false);
            return;
        }
        P.target = target;
        const start = target.selectionStart ?? target.value.length;
        const end = target.selectionEnd ?? start;
        const next = target.value.slice(0, start) + text + target.value.slice(end);
        const max = target.maxLength > 0 ? target.maxLength : 0;
        if (max && next.length > max) {
            toast("That would go over " + max + " characters", true);
            return;
        }
        target.setRangeText(text, start, end, "end");
        target.dispatchEvent(new Event("input", { bubbles: true }));
        remember(text);
    }

    function remember(text) {
        pushConfig((c) => {
            const list = (c.ChatOptions.emoji_recent || []).filter((x) => x !== text);
            c.ChatOptions.emoji_recent = [text].concat(list).slice(0, RECENT_MAX);
        });
    }

    function toggleFavorite(text) {
        const favs = chatOptions().emoji_favorites || [];
        const on = favs.includes(text);
        if (!on && favs.length >= FAVORITE_MAX) {
            toast("You can star up to " + FAVORITE_MAX + "; unstar one first", true);
            return;
        }
        const ok = pushConfigNow((c) => {
            const list = (c.ChatOptions.emoji_favorites || []).filter((x) => x !== text);
            c.ChatOptions.emoji_favorites = on ? list : [text].concat(list).slice(0, FAVORITE_MAX);
        });
        if (!ok) return;
        // Show the change before the server's copy comes back.
        const opts = chatOptions();
        opts.emoji_favorites = on ? favs.filter((x) => x !== text) : [text].concat(favs);
        P.hovered = text;
        const scroll = P.body.scrollTop;
        if (P.mode === "saved" && !P.search.value.trim()) {
            paint();
            P.body.scrollTop = scroll;
        } else {
            P.body.querySelectorAll(".ep-item").forEach((b) => {
                if (b.dataset.c === text) b.classList.toggle("starred", !on);
            });
            showName();
        }
        toast(on ? "Unstarred " + text : "Starred " + text);
    }

    document.addEventListener("click", (ev) => {
        const button = ev.target.closest("[data-emoji-for]");
        if (button) openPicker(button);
    });
    document.addEventListener("focusin", (ev) => {
        const input = ev.target.closest && ev.target.closest("#msgList .msg-input");
        if (input) P.msgIndex = Number(input.dataset.msg);
    });

    /* ================= session export ================= */

    const EXPORT_LABEL = {
        messages: "messages",
        chatbox: "chatbox history",
        activity: "VRChat activity",
        all: "session"
    };

    async function saveExport(what, format) {
        let blob, name;
        try {
            const response = await fetch("/export/session?what=" + encodeURIComponent(what) + "&format=" + encodeURIComponent(format), { cache: "no-store" });
            if (!response.ok) throw new Error(String(response.status));
            blob = await response.blob();
            const match = /filename="([^"]+)"/.exec(response.headers.get("Content-Disposition") || "");
            name = match ? match[1] : "fern-" + what + "." + format;
        } catch (e) {
            toast("Couldn't export the " + EXPORT_LABEL[what], true);
            return;
        }
        // A save dialog where the browser has one; the Downloads folder otherwise.
        if (typeof window.showSaveFilePicker === "function") {
            try {
                const handle = await window.showSaveFilePicker({
                    suggestedName: name,
                    types: [format === "csv"
                        ? { description: "CSV spreadsheet", accept: { "text/csv": [".csv"] } }
                        : { description: "Text file", accept: { "text/plain": [".txt"] } }]
                });
                const out = await handle.createWritable();
                await out.write(blob);
                await out.close();
                toast("Saved " + handle.name);
                return;
            } catch (e) {
                if (e && e.name === "AbortError") return;
            }
        }
        const url = URL.createObjectURL(blob);
        const link = document.createElement("a");
        link.href = url;
        link.download = name;
        document.body.appendChild(link);
        link.click();
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 2000);
        toast("Saved " + name + " to your Downloads folder");
    }

    document.addEventListener("click", (ev) => {
        const button = ev.target.closest("[data-export]");
        if (!button) return;
        let what = button.dataset.export;
        if (what === "pick") {
            const select = document.getElementById("exportWhat");
            what = select ? select.value : "all";
        }
        saveExport(what, button.dataset.format === "csv" ? "csv" : "txt");
    });

    function renderExport(st) {
        const desc = document.getElementById("exportDesc");
        if (!desc) return;
        const logs = st.logs || {};
        const hist = st.history || [];
        desc.textContent = "Everything since Fern started, as a text file or a spreadsheet. " +
            "The lists above show the latest " + (logs.activity || []).length + " events and " + hist.length + " messages; the file has the whole session.";
        const keep = chatOptions().keep_history !== false;
        const hint = document.getElementById("exportHint");
        if (hint) {
            hint.hidden = keep;
            hint.textContent = "Remembering sent messages is off on the Live page's settings, so messages typed there are left out.";
        }
    }

    /* ================= first-run setup ================= */

    const PRESETS = [
        ["music", "Music", ["media", "lyrics", "time"]],
        ["chill", "Chill", ["time", "text", "afk"]],
        ["social", "Social", ["world", "logs", "afk", "time"]],
        ["system", "PC stats", ["time", "cpu", "gpu", "memory"]],
        ["vr", "VR", ["time", "battery", "vrperf", "heartrate"]]
    ];
    const STEPS = ["welcome", "osc", "test", "lines", "done"];

    const W = {
        step: 0,
        shown: false,
        lines: null,     // Set of chosen line ids
        answer: "",      // "yes" or "no" after the test
        finishing: false
    };

    function dialog() { return document.getElementById("setupDialog"); }

    function openSetup() {
        const d = dialog();
        if (!d || d.open) return;
        W.step = 0;
        W.answer = "";
        W.lines = null;
        W.finishing = false;
        paintSetup(S.state);
        d.showModal();
    }

    function finishSetup(message) {
        W.finishing = true;
        send({ type: "setup", action: "finish" });
        const d = dialog();
        if (d && d.open) d.close();
        if (message) toast(message);
    }

    function setStep(i) {
        W.step = Math.max(0, Math.min(STEPS.length - 1, i));
        paintSetup(S.state);
        const d = dialog();
        const first = d && d.querySelector(".setup-step:not([hidden]) button, .setup-step:not([hidden]) input");
        if (first) first.focus({ preventScroll: true });
    }

    function check(id, state, title, detail) {
        const row = document.getElementById(id);
        if (!row) return;
        row.dataset.state = state;
        const icon = row.querySelector("i");
        icon.className = "fa " + (state === "ok" ? "fa-circle-check" : state === "bad" ? "fa-circle-xmark" : state === "warn" ? "fa-circle-exclamation" : "fa-circle-question");
        row.querySelector("b").textContent = title;
        row.querySelector("span").textContent = detail;
    }

    function ago(ms) {
        if (!ms) return "";
        const sec = Math.max(0, Math.round((Date.now() - ms) / 1000));
        if (sec < 60) return sec + "s ago";
        if (sec < 3600) return Math.round(sec / 60) + " min ago";
        if (sec < 86400) return Math.round(sec / 3600) + " h ago";
        return Math.round(sec / 86400) + " days ago";
    }

    function paintChecks(st) {
        const vr = st.vrchat || {};
        const su = st.setup || {};
        const oscIn = st.oscIn || {};
        check("setupVRChat", vr.running ? "ok" : "warn",
            vr.running ? "VRChat is running" : "VRChat isn't running",
            vr.running ? "Fern sees VRChat.exe." : "Start VRChat to finish the checks; Fern works the moment it opens.");

        const setting = su.oscSetting;
        check("setupSetting", setting === "on" ? "ok" : setting === "off" ? "bad" : "unknown",
            setting === "on" ? "OSC is switched on in VRChat" : setting === "off" ? "OSC is switched off in VRChat" : "VRChat's OSC switch",
            setting === "off" ? "Open the Action Menu in VRChat, then Options › OSC › Enabled." :
                setting === "on" ? "VRChat saved its OSC setting as on." :
                    "Couldn't read VRChat's saved setting. In VRChat: Action Menu › Options › OSC › Enabled.");

        const files = su.avatarConfigs || 0;
        check("setupFolder", su.oscFolder ? (files ? "ok" : "warn") : "unknown",
            su.oscFolder ? "VRChat's OSC folder is there" : "No OSC folder yet",
            su.oscFolder
                ? (files ? files + " avatar file" + (files === 1 ? "" : "s") + ", the newest written " + (ago(su.lastConfigMs) || "a while ago") + "." : "It has no avatar files yet; they appear once an avatar loads with OSC on.")
                : "VRChat makes it the first time OSC is on and an avatar loads.");

        const heard = vr.lastOscMs && Date.now() - vr.lastOscMs < 30000;
        const linked = vr.linked || heard;
        check("setupTraffic", linked ? "ok" : vr.running ? "warn" : "unknown",
            linked ? "VRChat is talking to Fern" : "No OSC from VRChat yet",
            linked ? "Fern received OSC " + (ago(vr.lastOscMs) || "just now") + (oscIn.port ? " on port " + oscIn.port : "") + "."
                : vr.running ? "Move or change a gesture in VRChat; if nothing arrives, check that OSC is on." : "Shows up once VRChat is running with OSC on.");
    }

    function paintLines(st) {
        const box = document.getElementById("setupLines");
        if (!box) return;
        const ids = ((st.setup || {}).lines || []).filter((id) => MODULE_NAME[id]);
        if (!W.lines) W.lines = new Set(ids.filter((id) => st.modules && st.modules[id]));
        const sig = ids.join(",") + "|" + Array.from(W.lines).sort().join(",");
        if (box.dataset.sig !== sig) {
            box.dataset.sig = sig;
            box.innerHTML = "";
            ids.forEach((id) => {
                const b = el("button", "setup-line" + (W.lines.has(id) ? " on" : ""));
                b.type = "button";
                b.dataset.line = id;
                b.setAttribute("aria-pressed", W.lines.has(id) ? "true" : "false");
                const m = MODULE_BY_ID[id] || {};
                b.innerHTML = '<i class="fa ' + (m.icon || "fa-circle") + '" aria-hidden="true"></i>';
                b.appendChild(el("span", "", MODULE_NAME[id]));
                if (m.desc) b.title = m.desc;
                box.appendChild(b);
            });
        }
        const presets = document.getElementById("setupPresets");
        if (presets && !presets.childElementCount) {
            PRESETS.forEach(([id, label]) => {
                const b = el("button", "btn ghost small", label);
                b.type = "button";
                b.dataset.preset = id;
                presets.appendChild(b);
            });
        }
        const n = W.lines.size;
        document.getElementById("setupLinesCount").textContent = n ? n + " line" + (n === 1 ? "" : "s") + " chosen" : "Pick at least one line";
        document.getElementById("setupApply").disabled = !n;
    }

    function paintSetup(st) {
        const d = dialog();
        if (!d || !st) return;
        const step = STEPS[W.step];
        d.querySelectorAll(".setup-step").forEach((s) => { s.hidden = s.dataset.step !== step; });
        d.querySelectorAll(".setup-dots i").forEach((dot, i) => {
            dot.classList.toggle("on", i === W.step);
            dot.classList.toggle("past", i < W.step);
        });
        document.getElementById("setupBack").hidden = W.step === 0 || step === "done";
        document.getElementById("setupSkip").hidden = step === "done";
        const next = document.getElementById("setupNext");
        next.hidden = step === "lines";
        document.getElementById("setupApply").hidden = step !== "lines";
        next.textContent = step === "welcome" ? "Start" : step === "done" ? "Finish" : "Next";

        if (step === "osc") paintChecks(st);
        if (step === "test") {
            const su = st.setup || {};
            const sent = !!su.testMs;
            const targets = su.testTargets || [];
            document.getElementById("setupTestInfo").textContent = sent
                ? "Sent " + ago(su.testMs) + " to " + (targets.join(", ") || "your OSC output") + ". Look at your chatbox in VRChat."
                : "Fern sends “Fern test message” to " + (st.osc || "127.0.0.1:9000") + ". VRChat has to be running with OSC on.";
            document.getElementById("setupAsk").hidden = !sent;
            document.getElementById("setupHelp").hidden = W.answer !== "no";
            document.getElementById("setupWorked").hidden = W.answer !== "yes";
            document.getElementById("setupTest").innerHTML = '<i class="fa fa-paper-plane" aria-hidden="true"></i> ' + (sent ? "Send again" : "Send a test message");
        }
        if (step === "lines") paintLines(st);
    }

    function wireSetup() {
        const d = dialog();
        if (!d) return;
        document.getElementById("setupNext").addEventListener("click", () => {
            if (STEPS[W.step] === "done") {
                finishSetup("");
                return;
            }
            setStep(W.step + 1);
        });
        document.getElementById("setupBack").addEventListener("click", () => setStep(W.step - 1));
        document.getElementById("setupSkip").addEventListener("click", () => finishSetup("Setup skipped. Run it again any time from the App page."));
        d.addEventListener("cancel", (ev) => {
            ev.preventDefault();
            finishSetup("Setup skipped. Run it again any time from the App page.");
        });
        document.getElementById("setupTest").addEventListener("click", () => {
            if (send({ type: "setup", action: "test" })) W.answer = "";
        });
        d.addEventListener("click", (ev) => {
            const answer = ev.target.closest("[data-answer]");
            if (answer) {
                W.answer = answer.dataset.answer;
                paintSetup(S.state);
                return;
            }
            const line = ev.target.closest("[data-line]");
            if (line) {
                const id = line.dataset.line;
                if (W.lines.has(id)) W.lines.delete(id); else W.lines.add(id);
                paintSetup(S.state);
                return;
            }
            const preset = ev.target.closest("[data-preset]");
            if (preset) {
                const p = PRESETS.find((x) => x[0] === preset.dataset.preset);
                if (p) W.lines = new Set(p[2]);
                paintSetup(S.state);
            }
        });
        document.getElementById("setupApply").addEventListener("click", () => {
            const ids = Array.from(W.lines || []);
            if (!ids.length) return;
            if (!send({ type: "setup", action: "lines", order: ids })) return;
            if (typeof forgetPendingEdits === "function") forgetPendingEdits();
            setStep(STEPS.indexOf("done"));
        });
        document.getElementById("setupOpen") && document.getElementById("setupOpen").addEventListener("click", openSetup);
    }

    function renderSetup(st) {
        const su = st.setup;
        if (!su) return;
        noticeToast("setup", su.notice);
        // The first launch opens the setup by itself, once.
        if (!W.shown && !su.done && !W.finishing) {
            W.shown = true;
            openSetup();
        }
        W.shown = true;
        const d = dialog();
        if (d && d.open) paintSetup(st);
        const badge = document.getElementById("setupBadge");
        if (badge) badge.textContent = su.done ? "done" : "not finished";
    }

    wireSetup();

    window.FernExtras = {
        render(st) {
            renderExport(st);
            renderSetup(st);
            if (P.root && !P.root.hidden && P.target && !P.target.isConnected) {
                const again = P.key ? resolveTarget(P.key) : null;
                if (again) P.target = again; else closePicker(false);
            }
        },
        openSetup: openSetup,
        searchItems: searchItems
    };
    // A state that arrived before this script loaded.
    if (S.state) window.FernExtras.render(S.state);
})();
