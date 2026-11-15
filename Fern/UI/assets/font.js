/* Fern interface font. Loaded in <head> by the panel and the CSS editor so the
   chosen font is in place before the first frame, and kept in sync between
   the two windows through localStorage.

   fern_font        {"source": "system", "family": "Bahnschrift"}
                    {"source": "upload"}   (absent or "default": theme font)
   fern_font_upload {"name": "Inter", "size": 123456, "v": 1700000000000}
                    The file itself is kept by Fern at /fonts/custom. */
(function () {
    const KEY = "fern_font";
    const UPLOAD_KEY = "fern_font_upload";
    const UPLOAD_FAMILY = "Fern Uploaded Font";
    const root = document.documentElement;
    let face = null;
    let faceVersion = null;

    const quote = (name) => '"' + String(name).replace(/\\/g, "\\\\").replace(/"/g, '\\"') + '"';

    // The theme's own stack, read from theme.css, so a chosen font still falls
    // back to it for characters it lacks.
    function fallback() {
        const saved = root.style.getPropertyValue("--font-ui");
        root.style.removeProperty("--font-ui");
        const stack = getComputedStyle(root).getPropertyValue("--font-ui").trim();
        if (saved) root.style.setProperty("--font-ui", saved);
        return stack || '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';
    }
    let base = null;
    const stack = (family) => quote(family) + ", " + (base || (base = fallback()));

    function readJSON(key) {
        try {
            const value = JSON.parse(localStorage.getItem(key) || "null");
            return value && typeof value === "object" ? value : null;
        } catch (e) {
            return null;
        }
    }

    const read = () => readJSON(KEY) || { source: "default" };
    const readUpload = () => readJSON(UPLOAD_KEY);

    // Registers the uploaded file under a fixed family name. The promise
    // rejects if the file is missing or unreadable.
    function loadUpload() {
        const meta = readUpload();
        if (!meta) return Promise.reject(new Error("no uploaded font"));
        if (face && faceVersion === meta.v) return face.loaded;
        if (face) document.fonts.delete(face);
        face = new FontFace(UPLOAD_FAMILY, "url(/fonts/custom?v=" + encodeURIComponent(meta.v || 0) + ")", { display: "block" });
        faceVersion = meta.v;
        document.fonts.add(face);
        const loading = face.load();
        loading.catch(() => { });
        return loading;
    }

    function familyOf(setting) {
        if (setting && setting.source === "system" && setting.family) return setting.family;
        if (setting && setting.source === "upload" && readUpload()) return UPLOAD_FAMILY;
        return "";
    }

    function apply(setting) {
        const family = familyOf(setting);
        if (family === UPLOAD_FAMILY) loadUpload();
        if (family) root.style.setProperty("--font-ui", stack(family));
        else root.style.removeProperty("--font-ui");
    }

    // Stylesheets in <head> come before this script, so the theme's
    // variables are already known here.
    apply(read());

    window.addEventListener("storage", (ev) => {
        if (ev.key === KEY || ev.key === UPLOAD_KEY) apply(read());
    });

    window.fernFont = {
        KEY: KEY,
        UPLOAD_KEY: UPLOAD_KEY,
        UPLOAD_FAMILY: UPLOAD_FAMILY,
        quote: quote,
        stack: stack,
        baseStack: () => base || (base = fallback()),
        read: read,
        readUpload: readUpload,
        loadUpload: loadUpload,
        apply: apply
    };
})();
