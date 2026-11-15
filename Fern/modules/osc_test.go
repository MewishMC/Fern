package modules

import (
	"reflect"
	"testing"
)

func TestSanitizeExtraOutputs(t *testing.T) {
	c := DefaultConfig()
	c.OSCOptions.Extra = nil // configs from before extra outputs
	c.sanitize()
	if want := []OSCTarget{{Host: "127.0.0.1", Port: 9002}, {Host: "127.0.0.1", Port: 9003}}; !reflect.DeepEqual(c.OSCOptions.Extra, want) {
		t.Errorf("missing extras became %+v", c.OSCOptions.Extra)
	}

	c.OSCOptions.Extra = []OSCTarget{
		{Enabled: true, Host: "  192.168.1.20 ", Port: 9010},
		{Enabled: true, Host: "", Port: 70000},
		{Enabled: true, Host: "10.0.0.1", Port: 9000},
	}
	c.sanitize()
	want := []OSCTarget{
		{Enabled: true, Host: "192.168.1.20", Port: 9010},
		{Enabled: true, Host: "127.0.0.1", Port: 9003},
	}
	if !reflect.DeepEqual(c.OSCOptions.Extra, want) {
		t.Errorf("got %+v, want %+v", c.OSCOptions.Extra, want)
	}
}
