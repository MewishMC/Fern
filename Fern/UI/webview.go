package UI

import (
	_ "embed"
	"os"
	"slices"

	webview "github.com/webview/webview_go"
)

//go:embed home.html
var MainPage string

// StartWebview opens the native window pointed at the local UI server. It blocks
// until the window is closed, so it must run on the main thread.
func StartWebview(url string) {
	w := webview.New(slices.Contains(os.Args, "--debug"))
	defer os.Exit(0)

	w.Dispatch(func() {
		w.Navigate(url)
	})

	w.SetSize(1040, 760, webview.HintNone)
	w.SetSize(820, 600, webview.HintMin)
	w.SetTitle("Fern ｢フェルン｣")
	w.Run()
}
