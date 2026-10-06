// Package include carries mbt's own C headers (mbtcheck.h) into the binary.
// mbt 3 has no submodule to put them on the include path from, so it writes
// them into the project's .mbt/include before compiling.
package include

import "embed"

//go:embed *.h
var Headers embed.FS
