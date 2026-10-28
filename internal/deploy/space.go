package deploy

import (
	"fmt"
	"strconv"
	"strings"
)

// device is what the space arithmetic needs of a DASD type: the bytes one
// track holds (unkeyed records, the raw capacity) and its tracks per
// cylinder.
type device struct{ track, trkPerCyl int }

var devices = map[string]device{
	"3330": {13030, 19},
	"3350": {19069, 30},
	"3380": {47476, 15},
	"3390": {56664, 15},
}

// NeededTracks estimates the tracks that bytes of load modules take on dev.
// Deliberately high: a load module is written in blocks up to the library's
// block size, and a track rarely fills to its raw capacity (3350, BLKSIZE
// 15040: one block per track), so the bytes count at 4/3 and each member gets
// a track for its last, partial block. An IEBCOPY replace writes the new
// member behind the old one and never reuses the old space, so a member that
// exists already counts in full too.
func NeededTracks(dev string, bytes, members int) (int, bool) {
	d, ok := devices[dev]
	if !ok {
		return 0, false
	}
	t := (bytes*4/3 + d.track - 1) / d.track
	return t + members, true
}

// FreeTracks is the space left in an existing library, from the data set
// list: sizex (allocated, in spacu units) and used (percent). The secondary
// extents a library may still take are not counted -- the list does not name
// the secondary quantity -- so the answer errs low.
func FreeTracks(a map[string]string) (free, total int, ok bool) {
	d, known := devices[a["dev"]]
	size, err1 := strconv.Atoi(a["sizex"])
	used, err2 := strconv.Atoi(a["used"])
	if !known || err1 != nil || err2 != nil {
		return 0, 0, false
	}
	switch strings.ToUpper(a["spacu"]) {
	case "TRACKS":
		total = size
	case "CYLINDERS":
		total = size * d.trkPerCyl
	default:
		return 0, 0, false
	}
	return total * (100 - used) / 100, total, true
}

// DirBlocks sizes a library's directory for entries names (members and
// aliases): five names to the block with a half again as many for growth,
// ceil(entries * 1.5 / 5), and never fewer than 20 blocks. A block holds
// about six names; the user data a load module carries makes it fewer.
func DirBlocks(entries int) int {
	n := (entries*3 + 9) / 10
	if n < 20 {
		n = 20
	}
	return n
}

// NewLibrarySpace sizes a library mbt allocates itself: room for several
// deploys before a compress, a secondary for growth, and a directory for
// entries names (members and aliases).
func NewLibrarySpace(needed, entries int) []any {
	primary := needed * 4
	if primary < 30 {
		primary = 30
	}
	secondary := needed
	if secondary < 15 {
		secondary = 15
	}
	return []any{"TRK", primary, secondary, DirBlocks(entries)}
}

// ReallocSpace sizes the library --reallocate allocates in place of the one
// described by a (its data set list attributes): what the modules need, as
// NewLibrarySpace has it, and never smaller than the old library. The old
// size is a floor, not a base: multiplying it again on every run made the
// library grow fourfold per --reallocate until the volume refused it.
// The data set list does not name the old directory size, so its floor is
// the directory the old library's oldEntries names call for.
func ReallocSpace(a map[string]string, bytes, members, entries, oldEntries int) []any {
	need, ok := NeededTracks(a["dev"], bytes, members)
	if !ok {
		need = 30
	}
	s := NewLibrarySpace(need, entries)
	if _, total, ok := FreeTracks(a); ok && total > s[1].(int) {
		s[1] = total
	}
	if d := DirBlocks(oldEntries); d > s[3].(int) {
		s[3] = d
	}
	return s
}

// Fill describes a library's use for the log line after a deploy.
func Fill(dsn string, a map[string]string) string {
	if _, total, ok := FreeTracks(a); ok {
		return fmt.Sprintf("%s: %s%% of %d tracks used, %s extent(s)", dsn, a["used"], total, a["extx"])
	}
	return fmt.Sprintf("%s: %s%% used", dsn, a["used"])
}
