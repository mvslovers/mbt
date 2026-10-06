package project

import (
	"os"
	"path/filepath"
)

// File names of the two schemas.
const (
	FileV3 = "mbt.toml"
	FileV2 = "project.toml"
)

// Find names the project file in root: mbt.toml or project.toml.  Both is an
// error -- mbt does not pick one.
func Find(root string) (string, error) {
	_, e3 := os.Stat(filepath.Join(root, FileV3))
	_, e2 := os.Stat(filepath.Join(root, FileV2))
	switch {
	case e3 == nil && e2 == nil:
		return "", configErr("both mbt.toml and project.toml are here -- a migrated project keeps mbt.toml only")
	case e3 == nil:
		return FileV3, nil
	case e2 == nil:
		return FileV2, nil
	}
	return "", configErr("no mbt.toml (or project.toml) in %s", root)
}

// Load reads the project at root, whichever schema it is in.
func Load(root string) (*Project, error) {
	f, err := Find(root)
	if err != nil {
		return nil, err
	}
	if f == FileV3 {
		return LoadV3(root, f)
	}
	p, err := LoadV2(root, f)
	if p != nil {
		p.Schema = 2
	}
	return p, err
}
