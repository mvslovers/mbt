package deploy

import (
	"strings"
	"testing"
)

func TestSpace(t *testing.T) {
	// UFSD.DEV.LINKLIB as RECEIVE left it on mvsdev: sized to its contents
	free, total, ok := FreeTracks(map[string]string{"dev": "3350", "spacu": "TRACKS", "sizex": "35", "used": "100"})
	if !ok || free != 0 || total != 35 {
		t.Errorf("full: %d/%d %v", free, total, ok)
	}
	// HTTPD.LINKLIB: 150 cylinders, 76% used
	free, total, ok = FreeTracks(map[string]string{"dev": "3350", "spacu": "CYLINDERS", "sizex": "150", "used": "76"})
	if !ok || total != 4500 || free != 1080 {
		t.Errorf("cylinders: %d/%d %v", free, total, ok)
	}
	if _, _, ok = FreeTracks(map[string]string{"dev": "9999", "spacu": "TRACKS", "sizex": "1", "used": "1"}); ok {
		t.Error("an unknown device was measured")
	}
	if _, _, ok = FreeTracks(map[string]string{"dev": "3350", "spacu": "BLOCKS", "sizex": "1", "used": "1"}); ok {
		t.Error("blocks were measured")
	}
	// 600000 bytes, 4 modules on a 3350: 800000/19069 -> 42 tracks, + 4
	if n, ok := NeededTracks("3350", 600000, 4); !ok || n != 46 {
		t.Errorf("needed %d %v", n, ok)
	}
	if n, _ := NeededTracks("3390", 600000, 4); n != 19 {
		t.Errorf("needed 3390 %d", n)
	}
	if s := NewLibrarySpace(5); s[1] != 30 || s[2] != 15 || s[3] != 20 {
		t.Errorf("small %v", s)
	}
	if s := NewLibrarySpace(46); s[1] != 184 || s[2] != 46 {
		t.Errorf("large %v", s)
	}
}

// The allocation step: every card within column 71, even for a 44-character
// name, and the volume named.
func TestAllocStep(t *testing.T) {
	dsn := "ABCDEFGH.ABCDEFGH.ABCDEFGH.ABCDEFGH.ABCDEFG"
	st := (&Alloc{Volume: "PUB000", Space: NewLibrarySpace(46)}).step(dsn)
	for _, l := range strings.Split(strings.TrimRight(st, "\n"), "\n") {
		if len(l) > 71 {
			t.Errorf("%d columns: %s", len(l), l)
		}
	}
	if !strings.Contains(st, "VOL=SER=PUB000,") || !strings.Contains(st, "SPACE=(TRK,(184,46,20))") {
		t.Errorf("%s", st)
	}
	if (*Alloc)(nil).step(dsn) != "" {
		t.Error("a nil Alloc made a step")
	}
}
