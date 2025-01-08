package modules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

var Config config

type config struct {
	TextOptions struct {
		LOADING      bool
		TIME         bool
		TEXT         bool
		MEM          bool
		ActiveWindow bool
	}
	LOGS bool
}

func init() {
	go func() {
		if err := loadConfig(); err != nil {
			if err.Error() == "unexpected end of JSON input" {
				_ = os.Remove("./config.json")
				Config = defaultConfig
			} else {
				panic(err)
			}
		}
		for {
			if err := saveConfig(); err != nil {
				if err.Error() == "config updated" {
					fmt.Println("config updated")
					if err := loadConfig(); err != nil {
						panic(err)
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
	TextOptions: struct {
		LOADING      bool
		TIME         bool
		TEXT         bool
		MEM          bool
		ActiveWindow bool
	}{
		true,
		true,
		true,
		true,
		true,
	},
	LOGS: true,
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
	return nil
}
