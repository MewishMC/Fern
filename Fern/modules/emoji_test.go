package modules

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCleanEmojiList(t *testing.T) {
	got := cleanEmojiList([]string{" ✨ ", "", "✨", "(◕‿◕)", "a\nb", "bell\x07", strings.Repeat("x", emojiMaxRunes+1), "\xff", "👨‍👩‍👧"}, 10)
	want := []string{"✨", "(◕‿◕)", "👨‍👩‍👧"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	many := make([]string, 50)
	for i := range many {
		many[i] = fmt.Sprint(i)
	}
	if got := cleanEmojiList(many, 5); len(got) != 5 || got[0] != "0" {
		t.Errorf("limit: got %q", got)
	}
	if got := cleanEmojiList(nil, 5); got == nil {
		t.Error("nil list should become an empty list")
	}
}

func TestSanitizeBoundsEmojiLists(t *testing.T) {
	c := DefaultConfig()
	if c.ChatOptions.EmojiRecent == nil || c.ChatOptions.EmojiFavorites == nil {
		t.Fatal("defaults should be empty lists, not null")
	}
	for i := 0; i < 100; i++ {
		c.ChatOptions.EmojiRecent = append(c.ChatOptions.EmojiRecent, fmt.Sprint("r", i))
		c.ChatOptions.EmojiFavorites = append(c.ChatOptions.EmojiFavorites, fmt.Sprint("f", i))
	}
	c.ChatOptions.EmojiFavorites = append([]string{"f1"}, c.ChatOptions.EmojiFavorites...)
	c.sanitize()
	if n := len(c.ChatOptions.EmojiRecent); n != EmojiRecentLimit {
		t.Errorf("%d recent emoji kept, want %d", n, EmojiRecentLimit)
	}
	if n := len(c.ChatOptions.EmojiFavorites); n != EmojiFavoriteLimit {
		t.Errorf("%d favourites kept, want %d", n, EmojiFavoriteLimit)
	}
	if c.ChatOptions.EmojiFavorites[0] != "f1" || c.ChatOptions.EmojiFavorites[1] != "f0" {
		t.Errorf("repeat not dropped: %q", c.ChatOptions.EmojiFavorites[:3])
	}
	// clone must not share the lists.
	d := c.clone()
	d.ChatOptions.EmojiRecent[0] = "changed"
	if c.ChatOptions.EmojiRecent[0] == "changed" {
		t.Error("clone shares the recent emoji list")
	}
}

func TestProfilesKeepEmojiLists(t *testing.T) {
	cur := DefaultConfig()
	cur.ChatOptions.EmojiRecent = []string{"✨"}
	cur.ChatOptions.EmojiFavorites = []string{"♡"}
	dst := DefaultConfig()
	keepMachineSettings(&dst, cur)
	if !reflect.DeepEqual(dst.ChatOptions.EmojiRecent, []string{"✨"}) || !reflect.DeepEqual(dst.ChatOptions.EmojiFavorites, []string{"♡"}) {
		t.Errorf("switching profiles lost the emoji lists: %+v", dst.ChatOptions)
	}
}
