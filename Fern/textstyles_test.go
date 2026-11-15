package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTextStyleScriptCarriesTables(t *testing.T) {
	rec := httptest.NewRecorder()
	serveTextStyleJS(rec, httptest.NewRequest("GET", "/textstyle.js", nil))
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("content type = %q", ct)
	}
	for _, want := range []string{"window.FernTextStyle", `"smallcaps"`, "ᴀ", `"Small caps"`} {
		if !strings.Contains(body, want) {
			t.Errorf("script lacks %q", want)
		}
	}
}
