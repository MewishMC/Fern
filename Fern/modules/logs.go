package modules

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	peopleJoined []string
	peopleLeft   []string
	activityLog  []string
)

func init() {
	SearchForPeople()
}

func getLatestLogFile() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %v", err)
	}

	logDir := filepath.Join(homeDir, "AppData", "LocalLow", "VRChat", "VRChat")
	fmt.Println("Checking log directory:", logDir)

	// Find all .txt log files
	files, err := filepath.Glob(filepath.Join(logDir, "*.txt"))
	if err != nil {
		return "", fmt.Errorf("error searching log files in %s: %v", logDir, err)
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no log files found in %s", logDir)
	}

	// Find the latest modified file
	var latestFile string
	var latestModTime time.Time

	for _, file := range files {
		info, err := os.Stat(file)
		if err == nil {
			modTime := info.ModTime()
			if latestFile == "" || modTime.After(latestModTime) {
				latestFile = file
				latestModTime = modTime
			}
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no valid log files found in %s", logDir)
	}

	return latestFile, nil
}

func SearchForPeople() {
	logFile, err := getLatestLogFile()
	if err != nil {
		fmt.Println("Error finding log file:", err)
		return
	}

	file, err := os.Open(logFile)
	if err != nil {
		fmt.Println("Unable to open file:", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	var tempActivityLog []string

	for scanner.Scan() {
		line := scanner.Text()
		processLogLine(line, "OnPlayerJoined", &peopleJoined, "has joined", &tempActivityLog)
		processLogLine(line, "OnPlayerLeft", &peopleLeft, "has left", &tempActivityLog)
	}

	activityLog = append(tempActivityLog, activityLog...)

	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading file:", err)
	}
}

func processLogLine(line, keyword string, list *[]string, action string, tempActivityLog *[]string) {
	if strings.Contains(line, keyword) {
		startIdx := strings.Index(line, keyword) + len(keyword)
		endIdx := strings.Index(line, "(usr_")

		if startIdx != -1 && endIdx != -1 {
			player := strings.TrimSpace(line[startIdx:endIdx])
			if !contains(*list, player) {
				*list = append(*list, player)
				*tempActivityLog = append(*tempActivityLog, fmt.Sprintf("[%s] %s", player, action))
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
		return "No recent activity"
	}
	return activityLog[0]
}

func GetLastJoinLogs() string {
	return formatLogs(peopleJoined, "has joined")
}

func GetLastLeaveLogs() string {
	return formatLogs(peopleLeft, "has left")
}

func formatLogs(list []string, action string) string {
	count := len(list)
	if count == 0 {
		return "No recent activity"
	}

	var logs []string
	for i := count - 1; i >= 0 && len(logs) < 20; i-- {
		logs = append(logs, fmt.Sprintf("(%s) %s", list[i], action))
	}

	return strings.Join(logs, "\n")
}
