package main

import (
	"net/http"
	"time"

	"Fern/modules"
)

// handleSessionExport serves this session's messages or VRChat activity as a
// .txt or .csv download: GET /export/session?what=messages|chatbox|activity|all&format=txt|csv.
// Like the settings export it only answers the panel itself.
func handleSessionExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !localRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	q := r.URL.Query()
	format := q.Get("format")
	data, name, err := modules.ExportSession(q.Get("what"), format, time.Now())
	if err != nil {
		http.Error(w, "unknown export", http.StatusBadRequest)
		return
	}
	contentType := "text/plain; charset=utf-8"
	if format == modules.ExportCSV {
		contentType = "text/csv; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// name is built from the checked export and format, so it is safe to quote.
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	_, _ = w.Write(data)
}
