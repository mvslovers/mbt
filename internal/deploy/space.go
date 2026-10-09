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

// NewLibrarySpace sizes a library mbt allocates itself: room for several
// deploys before a compress, a secondary for growth, and directory blocks
// for many members and aliases (six names per block).
func NewLibrarySpace(needed int) []any {
	primary := needed * 4
	if primary < 30 {
		primary = 30
	}
	secondary := needed
	if secondary < 15 {
		secondary = 15
	}
	return []any{"TRK", primary, secondary, 20}
}

// Fill describes a library's use for the log line after a deploy.
func Fill(dsn string, a map[string]string) string {
	if _, total, ok := FreeTracks(a); ok {
		return fmt.Sprintf("%s: %s%% of %d tracks used, %s extent(s)", dsn, a["used"], total, a["extx"])
	}
	return fmt.Sprintf("%s: %s%% used", dsn, a["used"])
}
