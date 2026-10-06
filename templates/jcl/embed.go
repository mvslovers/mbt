// Package jcl carries mbt's JCL templates into the binary; mbt 2 reads the
// same files from disk.
package jcl

import "embed"

//go:embed *.tpl
var Templates embed.FS
