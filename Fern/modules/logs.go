package modules

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
)

var (
	peopleJoined []string
	peopleLeft   []string
	activityLog  []string
)

func getLatestLogFile() (string, error) {
	logDir := fmt.Sprintf("%s\\AppData\\LocalLow\\VRChat\\VRChat\\", os.Getenv("USERPROFILE"))

	files, err := ioutil.ReadDir(logDir)
	if err != nil {
		return "", err
	}

	var latestFile os.FileInfo
	for _, file := range files {
		if !file.IsDir() {
			if latestFile == nil || file.ModTime().After(latestFile.ModTime()) {
				latestFile = file
			}
		}
	}

	if latestFile == nil {
		return "", fmt.Errorf("no log files found")
	}

	return filepath.Join(logDir, latestFile.Name()), nil
}

func SearchForPeople() {
	logFile, err := getLatestLogFile()
	if err != nil {
		fmt.Println("Error finding log file:", err)
		return
	}

	data, err := ioutil.ReadFile(logFile)
	if err != nil {
		fmt.Println("Unable to open file:", err)
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.Contains(line, "OnPlayerJoined") {
			index := strings.Index(line, "(usr_")
			if index != -1 {
				player := strings.TrimSpace(line[strings.Index(line, "OnPlayerJoined")+15 : index])
				if !contains(peopleJoined, player) {
					peopleJoined = append(peopleJoined, player)
					activityLog = append(activityLog, fmt.Sprintf("[%s] Has joined", player))
				}
			}
		}

		if strings.Contains(line, "OnPlayerLeft") {
			index := strings.Index(line, "(usr_")
			if index != -1 {
				player := strings.TrimSpace(line[strings.Index(line, "OnPlayerLeft")+13 : index])
				if !contains(peopleLeft, player) {
					peopleLeft = append(peopleLeft, player)
					activityLog = append(activityLog, fmt.Sprintf("[%s] Has left", player))
				}
			}
		}
	}
}

func contains(slice []string, value string) bool {
	for _, item := range slice {
		if item == value {
			return true
		}
	}
	return false
}

func getLastPlayerActivity() string {
	if len(activityLog) == 0 {
		return ""
	}
	return activityLog[max(0, len(activityLog)-1)]
}

func GetLastJoinLogs() string {
	var joinMessages []string
	for i := len(peopleJoined) - 1; i >= 0 && len(joinMessages) < 20; i-- {
		joinMessages = append(joinMessages, fmt.Sprintf("(%s) has joined", peopleJoined[i]))
	}

	return strings.Join(joinMessages, "\n")
}

func GetLastLeaveLogs() string {
	var formattedLogs []string
	for i := len(peopleLeft) - 1; i >= 0 && len(formattedLogs) < 20; i-- {
		formattedLogs = append(formattedLogs, fmt.Sprintf("(%s) has left", peopleLeft[i]))
	}

	return strings.Join(formattedLogs, "\n")
}
