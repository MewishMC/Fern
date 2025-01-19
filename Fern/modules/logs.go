package modules

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
)

const ignorePreLogged = true     // ignore all logs before the application was launched
const readSize = 1024 * 1024 * 2 // 2MB

var logFile *os.File
var logFilePath string
var preLoggedEnd int64 = 0

func ReadLastLogs() []string {
	logPath := GetLastLogPath()
	if logPath == "" {
		return nil
	}
	if logFilePath != logPath {
		logFilePath = logPath
		if logFile != nil {
			_ = logFile.Close()
		}
		f, err := os.Open(logPath)
		if err != nil {
			fmt.Printf("Error opening %s: %s\n", logFilePath, err.Error())
			return nil
		}
		logFile = f
		stat, err := f.Stat()
		if err != nil {
			fmt.Printf("Error stat-ing %s: %s\n", logFilePath, err.Error())
			return nil
		}
		if ignorePreLogged {
			preLoggedEnd = stat.Size()
		}
	}
	stat, err := logFile.Stat()
	if err != nil {
		fmt.Printf("Error stat-ing %s: %s\n", logFilePath, err.Error())
		return nil
	}
	size := max(min(readSize, stat.Size()-preLoggedEnd), 0)
	if _, err := logFile.Seek(-(size + 1), 2); err != nil {
		fmt.Printf("Error seeking to %d in %s: %s\n", -size, logFilePath, err.Error())
		return nil
	}
	buf := make([]byte, size)
	_, err = logFile.Read(buf)
	if err != nil {
		fmt.Printf("Error reading from %s: %s\n", logFilePath, err.Error())
		return nil
	}
	lines := strings.Split(string(buf), "\n")
	// map lines;
	for i, line := range lines {
		data := strings.Split(line, " - ")
		if len(data) < 2 {
			lines[i] = ""
			continue
		}
		lines[i] = strings.TrimSpace(data[1])
	}
	return lines
}

func GetLastLogPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		fmt.Println("APPDATA not set")
		return ""
	}
	pathV := path.Join(appData, "\\..\\LocalLow\\VRChat\\VRChat\\")
	files, err := os.ReadDir(pathV)
	if err != nil {
		fmt.Println(err.Error())
		return ""
	}
	var lastLogFileStat fs.FileInfo = nil
	for _, f := range files {
		if f.IsDir() || !strings.Contains(f.Name(), "output_log_") {
			continue
		}
		fInfo, err := f.Info()
		if err != nil {
			continue
		}
		if lastLogFileStat != nil && fInfo.ModTime().Before(lastLogFileStat.ModTime()) {
			continue
		}
		lastLogFileStat = fInfo
	}
	if lastLogFileStat == nil {
		fmt.Println("No log file found")
		return ""
	}
	return path.Join(pathV, lastLogFileStat.Name())
}
