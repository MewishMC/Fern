package modules

import (
	"encoding/json"
	"net/http"
	"strings"
)

// OSCQuery (https://github.com/Vidvox/OSCQueryProposal) is how VRChat finds
// OSC apps: it browses mDNS for _oscjson._tcp, asks each app's HTTP server
// for HOST_INFO (where to send OSC) and the node tree, and sends its avatar
// data to every app whose tree has /avatar. Fern answers with a small static
// tree; the data itself still arrives over plain UDP.

// OSC access values in the node tree.
const (
	oscAccessNone  = 0
	oscAccessWrite = 2
)

// oscQueryNode is one node of the OSCQuery tree.
type oscQueryNode struct {
	FullPath    string                   `json:"FULL_PATH"`
	Access      int                      `json:"ACCESS"`
	Type        string                   `json:"TYPE,omitempty"`
	Description string                   `json:"DESCRIPTION,omitempty"`
	Contents    map[string]*oscQueryNode `json:"CONTENTS,omitempty"`
}

// oscQueryHostInfo answers ?HOST_INFO.
type oscQueryHostInfo struct {
	Name       string          `json:"NAME"`
	OSCIP      string          `json:"OSC_IP"`
	OSCPort    int             `json:"OSC_PORT"`
	Transport  string          `json:"OSC_TRANSPORT"`
	Extensions map[string]bool `json:"EXTENSIONS"`
}

// oscQueryTree is what Fern says it listens to: the avatar change and
// avatar parameters, which is what makes VRChat send them.
func oscQueryTree() *oscQueryNode {
	return &oscQueryNode{
		FullPath: "/", Access: oscAccessNone, Description: "root node",
		Contents: map[string]*oscQueryNode{
			"avatar": {
				FullPath: "/avatar", Access: oscAccessNone,
				Contents: map[string]*oscQueryNode{
					"change": {FullPath: AvatarChangeAddr, Access: oscAccessWrite, Type: "s",
						Description: "The avatar ID, sent when the avatar changes"},
					"parameters": {FullPath: strings.TrimSuffix(AvatarParamPrefix, "/"), Access: oscAccessNone,
						Description: "Avatar parameters"},
				},
			},
		},
	}
}

// find walks the tree to a path such as /avatar/change.
func (n *oscQueryNode) find(path string) *oscQueryNode {
	cur := n
	for _, part := range strings.Split(strings.Trim(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, ok := cur.Contents[part]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// oscQueryHandler serves HOST_INFO and the node tree. oscPort reports the UDP
// port Fern is listening on.
func oscQueryHandler(name string, oscPort func() int) http.Handler {
	tree := oscQueryTree()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query := r.URL.RawQuery
		var body any
		switch {
		case query == "HOST_INFO":
			body = oscQueryHostInfo{
				Name: name, OSCIP: "127.0.0.1", OSCPort: oscPort(), Transport: "UDP",
				Extensions: map[string]bool{"ACCESS": true, "CLIPMODE": false, "RANGE": false, "TYPE": true, "VALUE": false},
			}
		default:
			node := tree.find(r.URL.Path)
			if node == nil {
				http.NotFound(w, r)
				return
			}
			if query == "" {
				body = node
				break
			}
			// A single attribute, e.g. ?TYPE. Unknown or empty ones have no content.
			attrs := map[string]any{"FULL_PATH": node.FullPath, "ACCESS": node.Access}
			if node.Type != "" {
				attrs["TYPE"] = node.Type
			}
			if node.Description != "" {
				attrs["DESCRIPTION"] = node.Description
			}
			if node.Contents != nil {
				attrs["CONTENTS"] = node.Contents
			}
			v, ok := attrs[query]
			if !ok {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			body = map[string]any{query: v}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
}
