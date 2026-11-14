package UI

import (
	"embed"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"sync"
	"syscall"

	webview "github.com/webview/webview_go"
)

//go:embed home.html
var MainPage string

// CSSEditorPage is the custom CSS editor, opened in its own window.
//
//go:embed css-editor.html
var CSSEditorPage string

//go:embed all:assets
var assets embed.FS

// Assets is the bundled stylesheet and icon font the page links to. They live
// inside the binary, so the interface needs no network at runtime.
func Assets() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil
	}
	return sub
}

var debug = slices.Contains(os.Args, "--debug")

// StartWebview opens the native window pointed at the local UI server. It blocks
// until the window is closed, so it must run on the main thread.
func StartWebview(url string) {
	w := webview.New(debug)
	defer os.Exit(0)

	// The page calls this to open the CSS editor as a native window. A popup
	// from window.open would get WebView2's own frame with the URL showing.
	_ = w.Bind("fernOpenCssEditor", func() { openEditorWindow(url + "css-editor") })

	w.Dispatch(func() {
		w.Navigate(url)
	})

	w.SetSize(1040, 760, webview.HintNone)
	w.SetSize(820, 600, webview.HintMin)
	w.SetTitle("Fern ｢フェルン｣")
	mainView = w
	hookClose(uintptr(w.Window()), closeMain)
	w.Run()
}

var (
	editorMu     sync.Mutex
	editorOpen   bool
	editorWindow uintptr // native handle once the window exists
)

// openEditorWindow shows the CSS editor in its own window, or brings the
// existing one forward. Each webview needs its own OS thread for its message
// loop, so the window lives on a locked goroutine until it is closed.
func openEditorWindow(url string) {
	editorMu.Lock()
	defer editorMu.Unlock()
	if editorOpen {
		focusWindow(editorWindow)
		return
	}
	editorOpen = true

	go func() {
		runtime.LockOSThread()
		w := webview.New(debug)
		defer func() {
			w.Destroy()
			editorMu.Lock()
			editorOpen, editorWindow = false, 0
			editorMu.Unlock()
		}()

		_ = w.Bind("fernCloseEditor", func() { w.Terminate() })
		w.SetTitle("Fern · Custom CSS")
		w.SetSize(720, 560, webview.HintNone)
		w.SetSize(480, 360, webview.HintMin)
		w.Navigate(url)

		hwnd := uintptr(w.Window())
		editorView = w
		hookClose(hwnd, closeEditor)
		editorMu.Lock()
		editorWindow = hwnd
		editorMu.Unlock()
		w.Run()
	}()
}

// webview only ends a window's run loop when the process's last window
// closes. With the panel and the editor both open, closing either one with its
// X button left that window's Run blocked: the editor stayed marked as open and
// could not be reopened, and closing the panel left Fern running with no panel.
// Catching WM_CLOSE and ending the loop ourselves fixes both.
var (
	comctl32              = syscall.NewLazyDLL("comctl32.dll")
	procSetWindowSubclass = comctl32.NewProc("SetWindowSubclass")
	procDefSubclassProc   = comctl32.NewProc("DefSubclassProc")
	closeHook             = syscall.NewCallback(closeWndProc)

	// Each is set and read only on its own window's thread.
	mainView   webview.WebView
	editorView webview.WebView
)

// Subclass ids, passed back to closeWndProc to say which window closed.
const (
	closeMain   = 1
	closeEditor = 2
	wmClose     = 0x0010
)

func hookClose(hwnd uintptr, which uintptr) {
	_, _, _ = procSetWindowSubclass.Call(hwnd, closeHook, which, 0)
}

func closeWndProc(hwnd, msg, wParam, lParam, which, _ uintptr) uintptr {
	if msg == wmClose {
		switch {
		case which == closeMain && mainView != nil:
			mainView.Terminate() // Run returns and StartWebview exits the app
			return 0
		case which == closeEditor && editorView != nil:
			editorView.Terminate() // Run returns and the deferred Destroy closes it
			return 0
		}
	}
	ret, _, _ := procDefSubclassProc.Call(hwnd, msg, wParam, lParam)
	return ret
}

var (
	user32                    = syscall.NewLazyDLL("user32.dll")
	procShowWindow            = user32.NewProc("ShowWindow")
	procSetForeground         = user32.NewProc("SetForegroundWindow")
	procIsIconic              = user32.NewProc("IsIconic")
	swRestore         uintptr = 9
)

func focusWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	if minimized, _, _ := procIsIconic.Call(hwnd); minimized != 0 {
		_, _, _ = procShowWindow.Call(hwnd, swRestore)
	}
	_, _, _ = procSetForeground.Call(hwnd)
}
