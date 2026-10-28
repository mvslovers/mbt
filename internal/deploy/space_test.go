package deploy

import (
	"strconv"
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
	if s := NewLibrarySpace(5, 3); s[1] != 30 || s[2] != 15 || s[3] != 20 {
		t.Errorf("small %v", s)
	}
	if s := NewLibrarySpace(46, 3); s[1] != 184 || s[2] != 46 {
		t.Errorf("large %v", s)
	}
}

// The allocation step: every card within column 71, even for a 44-character
// name, and the volume named.
func TestAllocStep(t *testing.T) {
	dsn := "ABCDEFGH.ABCDEFGH.ABCDEFGH.ABCDEFGH.ABCDEFG"
	st := (&Alloc{Volume: "PUB000", Space: NewLibrarySpace(46, 3)}).step(dsn)
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

// --reallocate run after run (#258): the new size follows what the modules
// need, with the old size as a floor. It used to be four times the old size,
// so five runs took a 3390 library from 32 to (32768,8192) tracks for 16
// modules of three tracks each, and the volume refused it.
func TestReallocSpace(t *testing.T) {
	bytes := 16 * 3 * 56664 * 3 / 4 // 16 modules, about three 3390 tracks each
	need, _ := NeededTracks("3390", bytes, 16)
	total := 32
	for run := 1; run <= 5; run++ {
		s := ReallocSpace(map[string]string{"dev": "3390", "spacu": "TRACKS", "sizex": strconv.Itoa(total), "used": "67"}, bytes, 16, 16, 0)
		want := 4 * need
		if total > want {
			want = total
		}
		if s[1] != want || s[2] != need {
			t.Fatalf("run %d from %d tracks: %v, want (%d,%d)", run, total, s, want, need)
		}
		total = s[1].(int)
	}
	if total != 4*need {
		t.Errorf("grew to %d tracks, want %d", total, 4*need)
	}
	// never smaller than it was: a large library keeps its size
	if s := ReallocSpace(map[string]string{"dev": "3390", "spacu": "TRACKS", "sizex": "900", "used": "10"}, bytes, 16, 16, 0); s[1] != 900 {
		t.Errorf("shrank: %v", s)
	}
	// an unknown device: the fallback, as for a new library
	if s := ReallocSpace(map[string]string{"dev": "9999"}, bytes, 16, 16, 0); s[1] != 120 || s[2] != 30 {
		t.Errorf("unknown device: %v", s)
	}
}

// The directory follows the names to deploy: ceil((members + aliases) * 1.5 / 5)
// blocks, 20 at least. 140 load modules took IEB148I in a library of 20.
func TestDirBlocks(t *testing.T) {
	for _, c := range []struct{ entries, want int }{
		{0, 20}, {1, 20}, {60, 20}, {66, 20}, {67, 21},
		{140, 42}, {141, 43}, {400, 120}, {10000, 3000},
	} {
		if got := DirBlocks(c.entries); got != c.want {
			t.Errorf("DirBlocks(%d) = %d, want %d", c.entries, got, c.want)
		}
	}
	// 140 modules, 10 aliases: 150 names, 45 blocks, in the new library
	if s := NewLibrarySpace(300, 150); s[3] != 45 {
		t.Errorf("new library for 150 names: %v", s)
	}
	st := (&Alloc{Volume: "PUB000", Space: NewLibrarySpace(300, 150)}).step("A.B.C")
	if !strings.Contains(st, "SPACE=(TRK,(1200,300,45))") {
		t.Errorf("%s", st)
	}
}

// --reallocate: the same directory as for a new library, and not smaller
// than what the old library's names call for (the list does not give its size).
func TestReallocDirBlocks(t *testing.T) {
	a := map[string]string{"dev": "3390", "spacu": "TRACKS", "sizex": "100", "used": "50"}
	bytes := 16 * 3 * 56664 * 3 / 4
	if s := ReallocSpace(a, bytes, 16, 140, 0); s[3] != 42 {
		t.Errorf("140 names, no old library: %v", s)
	}
	if s := ReallocSpace(a, bytes, 16, 16, 400); s[3] != 120 {
		t.Errorf("old library with 400 names: %v", s)
	}
	if s := ReallocSpace(a, bytes, 16, 300, 400); s[3] != 120 {
		t.Errorf("floor wins over the deploy: %v", s)
	}
	if s := ReallocSpace(a, bytes, 16, 500, 400); s[3] != 150 {
		t.Errorf("deploy wins over the floor: %v", s)
	}
}

// The probe allocates what the ALLOC step would, and lets it go again.
func TestProbeStep(t *testing.T) {
	a := &Alloc{Volume: "TSO001", Space: []any{"TRK", 300, 60, 20}}
	st := a.dd("PROBE", "HERC01.PL1C370.MBTPROBE", "(NEW,DELETE,DELETE)")
	for _, want := range []string{"//PROBE   EXEC PGM=IEFBR14", "DISP=(NEW,DELETE,DELETE)", "VOL=SER=TSO001,", "SPACE=(TRK,(300,60,20))"} {
		if !strings.Contains(st, want) {
			t.Errorf("missing %q in\n%s", want, st)
		}
	}
	if !strings.HasPrefix(a.step("X.Y"), "//ALLOC   EXEC PGM=IEFBR14\n") {
		t.Errorf("%s", a.step("X.Y"))
	}
}
