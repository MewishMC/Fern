package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

var Config config
var backupConfig config

type config struct {
	DisplayOptions struct {
		Show         bool `json:"show,omitempty"`
		Loading      bool `json:"loading,omitempty"`
		Time         bool `json:"time,omitempty"`
		Afk          bool `json:"afk,omitempty"`
		Media        bool `json:"media,omitempty"`
		Text         bool `json:"text,omitempty"`
		Memory       bool `json:"memory,omitempty"`
		ActiveWindow bool `json:"active_window,omitempty"`
		CPU          bool `json:"cpu,omitempty"`
		VRChatLogs   bool `json:"vr_chat_logs,omitempty"`
	} `json:"display_options"`

	TextMessages  map[string]string
	WindowOptions struct {
		Simplified bool `json:"simplified,omitempty"`
	}
	TextOptions struct {
		Cycle bool `json:"cycle"`
		Speed int  `json:"speed"`
	} `json:"TextOptions"`

	AfkOptions struct {
		Bypass     bool    `json:"bypass,omitempty"`
		AutoDetect bool    `json:"auto_detect,omitempty"`
		Poopyafk   bool    `json:"poopyafk,omitempty"`
		AFKTime    float64 `json:"afk_time,omitempty"`
		AFKMessage string  `json:"afk_message,omitempty"`
	}
	TimeoutMS int64 `json:"timeout_ms,omitempty"`
}

func init() {
	backupConfig = Config

	go func() {
		if err := loadConfig(); err != nil {
			if err.Error() == "unexpected end of JSON input" {
				_ = os.Remove("./config.json")
			}
			Config = backupConfig
			fmt.Println("Config reset to backup due to error:", err)
		}
		for {
			if err := saveConfig(); err != nil {
				if err.Error() == "config updated" {
					fmt.Println("config updated")
					if err := loadConfig(); err != nil {
						Config = backupConfig
						fmt.Println("Config reset to backup due to error:", err)
					}
					continue
				}
				panic(err)
			}
			time.Sleep(time.Second * 5)
		}
	}()
}

var startStat = time.Now()
var defaultConfig = config{
	DisplayOptions: struct {
		Show         bool `json:"show,omitempty"`
		Loading      bool `json:"loading,omitempty"`
		Time         bool `json:"time,omitempty"`
		Afk          bool `json:"afk,omitempty"`
		Media        bool `json:"media,omitempty"`
		Text         bool `json:"text,omitempty"`
		Memory       bool `json:"memory,omitempty"`
		ActiveWindow bool `json:"active_window,omitempty"`
		CPU          bool `json:"cpu,omitempty"`
		VRChatLogs   bool `json:"vr_chat_logs,omitempty"`
	}{
		Show:         true,
		Loading:      true,
		Time:         true,
		Afk:          true,
		Media:        true,
		Text:         true,
		Memory:       false,
		ActiveWindow: false,
		CPU:          false,
		VRChatLogs:   false,
	},

	WindowOptions: struct {
		Simplified bool `json:"simplified,omitempty"`
	}{
		Simplified: true,
	},
	TextMessages: make(map[string]string),
	TextOptions: struct {
		Cycle bool `json:"cycle"`
		Speed int  `json:"speed"`
	}{
		Cycle: true,
		Speed: 2500,
	},
	AfkOptions: struct {
		Bypass     bool    `json:"bypass,omitempty"`
		AutoDetect bool    `json:"auto_detect,omitempty"`
		Poopyafk   bool    `json:"poopyafk,omitempty"`
		AFKTime    float64 `json:"afk_time,omitempty"`
		AFKMessage string  `json:"afk_message,omitempty"`
	}{
		Bypass:     false,
		AutoDetect: true,
		AFKTime:    10,
		AFKMessage: "AFK",
	},
	TimeoutMS: 2500,
}

func loadConfig() error {
	bytesV, err := os.ReadFile("./config.json")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			Config = defaultConfig
			return nil
		}
		return err
	}
	fileInfo, err := os.Stat("./config.json")
	if err != nil {
		return err
	}
	startStat = fileInfo.ModTime()
	backupConfig = Config
	return json.Unmarshal(bytesV, &Config)
}

func saveConfig() error {
	fileInfo, err := os.Stat("./config.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if fileInfo != nil && startStat.Before(fileInfo.ModTime()) {
		return errors.New("config updated")
	}
	bytesV, _ := json.MarshalIndent(Config, "", " ")
	if err := os.WriteFile("./config.json", bytesV, 0644); err != nil {
		return err
	}
	stat, err := os.Stat("./config.json")
	if err != nil {
		return err
	}
	startStat = stat.ModTime()
	backupConfig = Config
	return nil
}
