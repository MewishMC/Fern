package main

import (
	"Fern/modules"
	"github.com/gorilla/websocket"
	"log"
	"net/http"
	"strings"
	"time"
)

var upgrader = websocket.Upgrader{}

func RegisterWebsocketHandler(path string, handler func(*websocket.Conn, error)) {
	http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("upgrade error: %v", err)
			return
		}
		handler(conn, err)
	})
}

func getDisplayOptions() map[string]bool {
	return map[string]bool{
		"Show":         modules.Config.DisplayOptions.Show,
		"media":        modules.Config.DisplayOptions.Media,
		"Loading":      modules.Config.DisplayOptions.Loading,
		"Time":         modules.Config.DisplayOptions.Time,
		"Afk":          modules.Config.DisplayOptions.Afk,
		"Media":        modules.Config.DisplayOptions.Media,
		"Text":         modules.Config.DisplayOptions.Text,
		"Memory":       modules.Config.DisplayOptions.Memory,
		"ActiveWindow": modules.Config.DisplayOptions.ActiveWindow,
		"CPU":          modules.Config.DisplayOptions.CPU,
		"VRChatLogs":   modules.Config.DisplayOptions.VRChatLogs,
	}
}

func GetAllActiveModules() {
	RegisterWebsocketHandler("/ws/modules", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		if err != nil {
			return
		}
		displayOptions := getDisplayOptions()
		conn.WriteJSON(displayOptions)
	})
}

func init() {
	GetAllActiveModules()
	updatePreview()
	updateLeaveLogs()
	updateJoinLogs()
	RegisterMessageHandler()

}

func updatePreview() {
	RegisterWebsocketHandler("/ws/preview", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		lastSentMessage := ""
		for {
			lastSentMessageReal := msg.Arguments[0].(string)
			lastSentMessageReal = strings.ReplaceAll(lastSentMessageReal, "\x03\xFE", "")

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
		for {
			time.Sleep(time.Second)
			leaveLogs := modules.GetLastLeaveLogs()
			if err := conn.WriteJSON(leaveLogs); err != nil {
				return
			}
		}
	})
}

func updateJoinLogs() {
	RegisterWebsocketHandler("/ws/logs/join", func(conn *websocket.Conn, err error) {
		defer conn.Close()
		for {

			time.Sleep(time.Second)
			joinLogs := modules.GetLastJoinLogs()
			if err := conn.WriteJSON(joinLogs); err != nil {
				return
			}
		}
	})
}

func RegisterMessageHandler() {
	RegisterWebsocketHandler("/ws/console", func(conn *websocket.Conn, err error) {
		if err != nil {
			return
		}
		listenForMessages(conn)
	})
}

func listenForMessages(conn *websocket.Conn) {
	defer func() {
		log.Println("Closing connection...")
		conn.Close()
	}()

	for {
		var msg map[string]interface{}
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Unexpected connection closure: %v", err)
			}
			return
		}

		text, ok := msg["text"].(string)
		if !ok {
			log.Println("Invalid message format: missing 'text' field")
			continue
		}

		switch text {
		case "ChangeShowOptions":
			modules.Config.DisplayOptions.Show = !modules.Config.DisplayOptions.Show
		case "ChangeMediaOptions":
			modules.Config.DisplayOptions.Media = !modules.Config.DisplayOptions.Media
		case "ChangeTextOptions":
			modules.Config.DisplayOptions.Text = !modules.Config.DisplayOptions.Text
		case "ChangeMemoryOptions":
			modules.Config.DisplayOptions.Memory = !modules.Config.DisplayOptions.Memory
		case "ChangeActiveWindowOptions":
			modules.Config.DisplayOptions.ActiveWindow = !modules.Config.DisplayOptions.ActiveWindow
		case "ChangeCPUOptions":
			modules.Config.DisplayOptions.CPU = !modules.Config.DisplayOptions.CPU
		case "ChangeVRChatLogsOptions":
			modules.Config.DisplayOptions.VRChatLogs = !modules.Config.DisplayOptions.VRChatLogs
		case "ChangeLoadingOptions":
			modules.Config.DisplayOptions.Loading = !modules.Config.DisplayOptions.Loading
		case "ChangeTimeOptions":
			modules.Config.DisplayOptions.Time = !modules.Config.DisplayOptions.Time
		case "ChangeAfkOptions":
			modules.Config.DisplayOptions.Afk = !modules.Config.DisplayOptions.Afk
		default:
			log.Printf("Unknown command: %s", text)
		}
	}
}
