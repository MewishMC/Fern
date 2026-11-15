package modules

import "testing"

func TestTidyTrack(t *testing.T) {
	cases := []struct {
		title, artist string
		wantT, wantA  string
	}{
		{"Rick Astley - Never Gonna Give You Up (Official Music Video)", "Rick Astley", "Never Gonna Give You Up", "Rick Astley"},
		{"The Weeknd - Blinding Lights (Official Video)", "TheWeekndVEVO", "Blinding Lights", "The Weeknd"},
		{"Queen – Bohemian Rhapsody (Official Video Remastered)", "Queen Official", "Bohemian Rhapsody (Official Video Remastered)", "Queen"},
		{"Bohemian Rhapsody [HD]", "Queen", "Bohemian Rhapsody", "Queen"},
		{"Someone You Loved (Lyrics)", "Lewis Capaldi", "Someone You Loved", "Lewis Capaldi"},
		{"Blinding Lights (Official Audio)", "The Weeknd - Topic", "Blinding Lights", "The Weeknd"},
		{"Bad Guy | Official Music Video", "Billie Eilish", "Bad Guy", "Billie Eilish"},
		{"Numb (Official Music Video) [4K UPGRADE]", "Linkin Park", "Numb [4K UPGRADE]", "Linkin Park"},
		{"Mr. Brightside (Official Video) [HD]", "The Killers", "Mr. Brightside", "The Killers"},
		{"【MV】 夜に駆ける", "YOASOBI", "夜に駆ける", "YOASOBI"},
		{"Despacito (Lyrics / Letra)", "Luis Fonsi", "Despacito", "Luis Fonsi"},
		{"Starboy (Official Lyric Video) ft. Daft Punk", "The Weeknd, Daft Punk", "Starboy", "The Weeknd, Daft Punk"},
		// Featured artists stay when the artist doesn't already name them.
		{"Shape of You (feat. Stormzy)", "Ed Sheeran, Stormzy", "Shape of You", "Ed Sheeran, Stormzy"},
		{"Shape of You (feat. Stormzy)", "Ed Sheeran", "Shape of You (feat. Stormzy)", "Ed Sheeran"},
		{"Levitating ft. DaBaby", "Dua Lipa & DaBaby", "Levitating", "Dua Lipa & DaBaby"},
		{"Under Pressure [with David Bowie]", "Queen, David Bowie", "Under Pressure", "Queen, David Bowie"},
		// Things that describe the song itself are kept.
		{"Creep (Acoustic Version)", "Radiohead", "Creep (Acoustic Version)", "Radiohead"},
		{"Yellow (Live at Glastonbury 2016)", "Coldplay", "Yellow (Live at Glastonbury 2016)", "Coldplay"},
		{"Anti-Hero - Taylor's Version", "Taylor Swift", "Anti-Hero - Taylor's Version", "Taylor Swift"},
		{"Other Artist - Song", "Uploader", "Other Artist - Song", "Uploader"},
		// Never empties the title.
		{"(Official Video)", "Someone", "(Official Video)", "Someone"},
		{"", "", "", ""},
	}
	for _, c := range cases {
		gotT, gotA := TidyTrack(c.title, c.artist)
		if gotT != c.wantT || gotA != c.wantA {
			t.Errorf("TidyTrack(%q, %q) = %q, %q; want %q, %q", c.title, c.artist, gotT, gotA, c.wantT, c.wantA)
		}
	}
}

func TestMediaLineTidies(t *testing.T) {
	o := DefaultConfig().MediaOptions
	o.ShowProgress = false
	info := MediaInfo{Available: true, Playing: true, Title: "Rick Astley - Never Gonna Give You Up (Official Music Video)", Artist: "Rick Astley"}
	if got := formatMediaLine(o, info); got != "♪ Never Gonna Give You Up — Rick Astley" {
		t.Errorf("tidy on: got %q", got)
	}
	o.TidyTitles = false
	if got := formatMediaLine(o, info); got != "♪ Rick Astley - Never Gonna Give You Up (Official Music Video) — Rick Astley" {
		t.Errorf("tidy off: got %q", got)
	}
}
