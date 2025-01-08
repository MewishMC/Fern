package modules

import (
	"fmt"
	"slices"
	"strings"
)

func GetJoinAndLeaveLogs() string {
	lastLogs := ReadLastLogs()
	var displayLogs strings.Builder

	for _, log := range lastLogs {
		switch {
		case strings.Contains(log, "OnPlayerLeft "):
			startIdx := strings.Index(log, "OnPlayerLeft ") + len("OnPlayerLeft ")
			endIdx := strings.Index(log[startIdx:], " (") + startIdx
			if endIdx > startIdx {
				playerName := strings.TrimSpace(log[startIdx:endIdx])
				displayLogs.WriteString(fmt.Sprintf("%s left the game\n", playerName))
			}

		case strings.Contains(log, "OnPlayerJoined "):
			startIdx := strings.Index(log, "OnPlayerJoined ") + len("OnPlayerJoined ")
			endIdx := strings.Index(log[startIdx:], " (") + startIdx
			if endIdx > startIdx {
				playerName := strings.TrimSpace(log[startIdx:endIdx])
				displayLogs.WriteString(fmt.Sprintf("%s joined the game\n", playerName))
			}
		}
	}

	lines := strings.Split(strings.TrimSuffix(displayLogs.String(), "\n"), "\n")

	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}

	return lines[0]
}

func GetJoinLogs() []string {
	lastLogs := ReadLastLogs()
	var displayLogs strings.Builder

	for _, log := range lastLogs {
		switch {
		case strings.Contains(log, "OnPlayerJoined "):

			startIdx := strings.Index(log, "OnPlayerJoined ") + len("OnPlayerJoined ")
			endIdx := strings.Index(log[startIdx:], " (") + startIdx
			if endIdx > startIdx {
				playerName := strings.TrimSpace(log[startIdx:endIdx])
				displayLogs.WriteString(fmt.Sprintf("%s joined the game\n", playerName))
			}
		}
	}

	lines := strings.Split(strings.TrimSuffix(displayLogs.String(), "\n"), "\n")

	slices.Reverse(lines)

	return lines
}

func GetLeaveLogs() []string {
	lastLogs := ReadLastLogs()
	var displayLogs strings.Builder

	for _, log := range lastLogs {
		switch {
		case strings.Contains(log, "OnPlayerLeft "):
			startIdx := strings.Index(log, "OnPlayerLeft ") + len("OnPlayerLeft ")
			endIdx := strings.Index(log[startIdx:], " (") + startIdx
			if endIdx > startIdx {
				playerName := strings.TrimSpace(log[startIdx:endIdx])
				displayLogs.WriteString(fmt.Sprintf("%s left the game\n", playerName))
			}
		}
	}

	lines := strings.Split(strings.TrimSuffix(displayLogs.String(), "\n"), "\n")
	slices.Reverse(lines)
	return lines
}
