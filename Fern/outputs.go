package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/hypebeast/go-osc/osc"

	"Fern/modules"
)

var (
	outMu   sync.Mutex
	outKey  string
	outList []*osc.Client // the main destination first, then enabled extras
	oscAddr atomic.Value  // label of the destinations, read by the UI server
)

// oscDestinations lists host:port of the main output and of every enabled
// extra one, skipping repeats so nobody gets the chatbox twice.
func oscDestinations(opts modules.OSCOptions) []string { return modules.OSCDestinations(opts) }

// oscClients returns a client per destination, rebuilt when they change.
func oscClients(opts modules.OSCOptions) []*osc.Client {
	dests := oscDestinations(opts)
	key := strings.Join(dests, ",")
	outMu.Lock()
	defer outMu.Unlock()
	if key != outKey || outList == nil {
		outList = outList[:0]
		for _, d := range dests {
			i := strings.LastIndex(d, ":")
			var port int
			fmt.Sscanf(d[i+1:], "%d", &port)
			outList = append(outList, osc.NewClient(d[:i], port))
		}
		outKey = key
		label := dests[0]
		if len(dests) > 1 {
			label += fmt.Sprintf(" +%d", len(dests)-1)
		}
		oscAddr.Store(label)
	}
	return append([]*osc.Client{}, outList...)
}

// sendOSC sends msg to every destination and reports how many took it.
func sendOSC(opts modules.OSCOptions, msg *osc.Message) int {
	sent := 0
	for _, c := range oscClients(opts) {
		if err := c.Send(msg); err == nil {
			sent++
		}
	}
	return sent
}

func oscTarget() string {
	if addr, ok := oscAddr.Load().(string); ok {
		return addr
	}
	return "127.0.0.1:9000"
}
