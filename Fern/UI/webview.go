package UI

import (
	_ "embed"
	"fmt"
	webview "github.com/webview/webview_go"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed home.html
var mainPage string

func StartWebview() {
	port := findOpenPort()
	if port == -1 {
		panic("No open ports found for webview")
	}
	fmt.Println("Using port " + strconv.Itoa(port))
	go func() {
		start := time.Now()
		fmt.Println("Starting server on port " + strconv.Itoa(port))

		http.HandleFunc("/", func(writer http.ResponseWriter, request *http.Request) {
			http.ServeContent(writer, request, "index.html", start, strings.NewReader(mainPage))
		})
		if err := http.ListenAndServe("127.0.0.1:"+strconv.Itoa(port), nil); err != nil {
			panic(err)
		}
	}()

	w := webview.New(slices.Contains(os.Args, "--debug"))

	defer os.Exit(0)

	w.Dispatch(func() {
		w.Navigate("http://127.0.0.1:" + strconv.Itoa(port) + "/")
	})

	w.SetSize(400, 200, webview.HintMin)
	w.SetTitle("Fern ｢フェルン｣")
	w.Run()
}

func findOpenPort() int {
	for i := 1932; i < 65535; i++ {
		conn, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(i))
		if err != nil {
			continue
		}
		_ = conn.Close()
		return i
	}
	return -1
}
