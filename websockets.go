package main

import (
	"VrChatBox/modules"
	"github.com/gorilla/websocket"
	"net/http"
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{}

func RegisterWebsocketHandler(path string, handler func(*websocket.Conn, error)) {
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		handler(upgrader.Upgrade(w, r, nil))
	})
}

func init() {
	updatePreview()
	updateLeaveLogs()
	updateJoinLogs()
}

func updatePreview() {
	RegisterWebsocketHandler("/ws/preview", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		lastSentMessage := ""
		for {
			lastSentMessageReal := msg.Arguments[0].(string)
			if lastSentMessage == lastSentMessageReal {
				continue
			}
			lastSentMessage = lastSentMessageReal
			if err := conn.WriteJSON(lastSentMessage); err != nil {
				return
			}
		}
	})
}

func updateLeaveLogs() {
	RegisterWebsocketHandler("/ws/logs/leave", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		var sentLogsN int
		for {
			leaveLogs := modules.GetLeaveLogs()
			leaveLogsN := len(leaveLogs)
			if sentLogsN == leaveLogsN {
				continue
			}
			if err := conn.WriteJSON(strings.Join(leaveLogs[sentLogsN:], "<br>")); err != nil {
				return
			}
			sentLogsN = leaveLogsN
			time.Sleep(time.Second)
		}
	})
}

func updateJoinLogs() {
	RegisterWebsocketHandler("/ws/logs/join", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		var sentLogsN int
		for {
			joinLogs := modules.GetJoinLogs()
			joinLogsN := len(joinLogs)
			if sentLogsN == joinLogsN {
				continue
			}
			if err := conn.WriteJSON(strings.Join(joinLogs[sentLogsN:], "<br>")); err != nil {
				return
			}
			sentLogsN = joinLogsN
			time.Sleep(time.Second)
		}
	})
}
