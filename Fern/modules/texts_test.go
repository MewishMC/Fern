package modules

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestOldTextMessagesLoad(t *testing.T) {
	old := []byte(`{"TextMessages":["hello","","bye"],"TextOptions":{"cycle":true,"speed":3000,"shuffle":true,"label":"♡"}}`)
	c := DefaultConfig()
	if err := json.Unmarshal(old, &c); err != nil {
		t.Fatalf("old config did not load: %v", err)
	}
	c.sanitize()
	want := []TextMessage{{Text: "hello", On: true}, {Text: "", On: true}, {Text: "bye", On: true}}
	if !slices.Equal(c.TextMessages, want) {
		t.Errorf("messages = %+v, want %+v", c.TextMessages, want)
	}
	if !c.TextOptions.Shuffle || c.TextOptions.Weighted || c.TextOptions.Group != "" || len(c.TextOptions.Groups) != 0 {
		t.Errorf("options = %+v", c.TextOptions)
	}

	// Saved again, it is the new format, and that loads back the same.
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	again := DefaultConfig()
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(again.TextMessages, c.TextMessages) {
		t.Errorf("round trip = %+v", again.TextMessages)
	}

	// Objects and strings may be mixed, and a missing "on" means on.
	var mixed []TextMessage
	if err := json.Unmarshal([]byte(`["a",{"text":"b","group":"Night"},{"text":"c","on":false}]`), &mixed); err != nil {
		t.Fatal(err)
	}
	want = []TextMessage{{Text: "a", On: true}, {Text: "b", On: true, Group: "Night"}, {Text: "c"}}
	if !slices.Equal(mixed, want) {
		t.Errorf("mixed = %+v, want %+v", mixed, want)
	}
}

func TestSanitizeTextGroups(t *testing.T) {
	c := DefaultConfig()
	c.TextMessages = []TextMessage{{Text: "a", On: true, Group: " Night "}, {Text: "b", On: true, Group: "day"}}
	c.TextOptions.Groups = []string{"Day", "", "night", "Empty"}
	c.TextOptions.Group = "gone"
	c.TextOptions.Icons = []string{" ✦ ", "", "♡"}
	c.sanitize()
	if !slices.Equal(c.TextOptions.Groups, []string{"Day", "night", "Empty"}) {
		t.Errorf("groups = %q", c.TextOptions.Groups)
	}
	if c.TextMessages[0].Group != "Night" {
		t.Errorf("group name not trimmed: %q", c.TextMessages[0].Group)
	}
	if c.TextOptions.Group != "" {
		t.Errorf("unknown group filter kept: %q", c.TextOptions.Group)
	}
	if !slices.Equal(c.TextOptions.Icons, []string{"✦", "♡"}) {
		t.Errorf("icons = %q", c.TextOptions.Icons)
	}
}

func rotationConfig(texts ...string) Config {
	c := DefaultConfig()
	c.TextMessages = Messages(texts...)
	c.TextOptions.Cycle = true
	c.TextOptions.Speed = 1000
	return c
}

// walk returns the positions shown over n time steps.
func walk(r *rotation, c Config, start time.Time, n int) []int {
	var out []int
	for k := 0; k < n; k++ {
		i, _ := r.current(c, start.Add(time.Duration(k)*time.Second+time.Millisecond))
		out = append(out, i)
	}
	return out
}

func TestRotationSkipsUnheartedAndOtherGroups(t *testing.T) {
	c := rotationConfig("a", "b", "c", "d", " ")
	c.TextMessages[1].On = false
	start := time.Unix(1000, 0)
	if got := walk(newRotation(start, 1), c, start, 6); !slices.Equal(got, []int{0, 2, 3, 0, 2, 3}) {
		t.Errorf("list order = %v", got)
	}

	c.TextMessages[0].Group = "Night"
	c.TextMessages[3].Group = "night"
	c.TextOptions.Group = "Night"
	if got := walk(newRotation(start, 1), c, start, 4); !slices.Equal(got, []int{0, 3, 0, 3}) {
		t.Errorf("group order = %v", got)
	}

	c.TextOptions.Cycle = false
	if got := walk(newRotation(start, 1), c, start, 3); !slices.Equal(got, []int{0, 0, 0}) {
		t.Errorf("no cycling = %v", got)
	}

	for i := range c.TextMessages {
		c.TextMessages[i].On = false
	}
	if i, _ := newRotation(start, 1).current(c, start); i != -1 {
		t.Errorf("nothing hearted still showed %d", i)
	}
}

func TestShuffleShowsEveryMessageOncePerPass(t *testing.T) {
	c := rotationConfig("a", "b", "c", "d", "e")
	c.TextOptions.Shuffle = true
	start := time.Unix(1000, 0)
	r := newRotation(start, 7)
	shown := walk(r, c, start, 25)
	for pass := 0; pass < 5; pass++ {
		got := slices.Clone(shown[pass*5 : pass*5+5])
		slices.Sort(got)
		if !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
			t.Fatalf("pass %d showed %v", pass, shown[pass*5:pass*5+5])
		}
	}
	for k := 1; k < len(shown); k++ {
		if shown[k] == shown[k-1] {
			t.Fatalf("same message twice in a row at %d: %v", k, shown)
		}
	}
}

func TestWeightedFavoursLongUnseen(t *testing.T) {
	c := rotationConfig("a", "b", "c")
	c.TextOptions.Shuffle = true
	c.TextOptions.Weighted = true
	now := time.Unix(100000, 0)
	counts := map[int]int{}
	r := newRotation(now, 3)
	for k := 0; k < 2000; k++ {
		r.index = 0
		r.lastShown = map[string]time.Time{"a": now, "b": now.Add(-10 * time.Second), "c": now.Add(-1000 * time.Second)}
		counts[r.next(c, []int{0, 1, 2}, now)]++
	}
	if counts[0] != 0 {
		t.Errorf("picked the current message %d times", counts[0])
	}
	if counts[2] < counts[1]*20 {
		t.Errorf("long unseen message not favoured: %v", counts)
	}
}

func TestIconsRotateWithMessages(t *testing.T) {
	c := rotationConfig("a", "b")
	c.TextOptions.Icons = []string{"✦", "♡", "☾"}
	start := time.Unix(1000, 0)
	r := newRotation(start, 1)
	var icons []string
	for k := 0; k < 4; k++ {
		at := start.Add(time.Duration(k)*time.Second + time.Millisecond)
		_, icon := r.current(c, at)
		_, same := r.current(c, at.Add(100*time.Millisecond))
		if icon != same {
			t.Fatalf("icon changed within one message: %q then %q", icon, same)
		}
		icons = append(icons, icon)
	}
	if !slices.Equal(icons, []string{"✦", "♡", "☾", "✦"}) {
		t.Errorf("icons = %q", icons)
	}

	// A lone message in the rotation still gets the next icon each step.
	lone := rotationConfig("only")
	lone.TextOptions.Icons = c.TextOptions.Icons
	r1 := newRotation(start, 1)
	_, a := r1.current(lone, start.Add(time.Millisecond))
	_, b := r1.current(lone, start.Add(time.Second+time.Millisecond))
	if a != "✦" || b != "♡" {
		t.Errorf("lone message icons = %q, %q", a, b)
	}

	c.TextOptions.IconsRandom = true
	prev := ""
	for k := 4; k < 30; k++ {
		_, icon := r.current(c, start.Add(time.Duration(k)*time.Second+time.Millisecond))
		if icon == prev {
			t.Fatalf("shuffled icon repeated: %q", icon)
		}
		prev = icon
	}
}

func TestEditingShownMessageKeepsIt(t *testing.T) {
	c := rotationConfig("a", "b", "c")
	start := time.Unix(1000, 0)
	r := newRotation(start, 1)
	first, _ := r.current(c, start.Add(time.Second+time.Millisecond))
	c.TextMessages[first].Text += "!"
	if again, _ := r.current(c, start.Add(time.Second+500*time.Millisecond)); again != first {
		t.Errorf("editing message %d moved the rotation to %d", first, again)
	}
}
