package main

import (
	"strconv"

	"Fern/modules"
)

// handleWebLinesCommand runs the calendar, price and Steam pages' actions.
//
//	{type:"calendar", action:"add", text:<link>}    stores a calendar link (encrypted, never echoed)
//	{type:"calendar", action:"remove", text:<index>}
//	{type:"calendar", action:"refresh"}
//	{type:"prices", action:"refresh"}
//	{type:"steam", action:"refresh"}
func handleWebLinesCommand(cmd command) {
	switch cmd.Type {
	case "calendar":
		switch cmd.Action {
		case "add":
			_ = modules.AddCalendarLink(cmd.Text)
		case "remove":
			if i, err := strconv.Atoi(cmd.Text); err == nil {
				_ = modules.RemoveCalendarLink(i)
			}
		case "refresh":
			modules.RefreshCalendar()
		}
	case "prices":
		if cmd.Action == "refresh" {
			modules.RefreshPrices()
		}
	case "steam":
		if cmd.Action == "refresh" {
			modules.RefreshSteam()
		}
	}
}
