package main

import (
	"encoding/json"
	"strings"
	"testing"

	"Fern/modules"
)

func TestVRInputCommandReportsWithoutSteamVR(t *testing.T) {
	before := modules.CurrentVRInput().NoticeSeq
	handleCommand(command{Type: "vrInput", Action: "test", Module: "joined"})
	st := modules.CurrentVRInput()
	if st.NoticeSeq != before+1 || !st.NoticeBad || !strings.Contains(st.Notice, "SteamVR") {
		t.Errorf("notice %+v", st)
	}
	handleCommand(command{Type: "vrInput", Action: "bogus"})
	if modules.CurrentVRInput().NoticeSeq != before+1 {
		t.Error("unknown action changed the notice")
	}
	data, err := json.Marshal(uiState{VRInput: modules.CurrentVRInput()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"vrInput":{"connected":false`) || !strings.Contains(string(data), `"params":[]`) {
		t.Errorf("state JSON %s", data)
	}
}
