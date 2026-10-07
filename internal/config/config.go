// Package config resolves the MVS connection settings with mbt v2's priority:
// the environment (MBT_*), the project's .env, ~/.mbt/config.toml, then the
// built-in defaults.  (Named targets replace this in mbt 3's design, section
// 7; until then the v2 sources are read unchanged.)
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

var defaults = map[string]string{
	"mvs.host": "localhost", "mvs.port": "1080", "mvs.user": "IBMUSER", "mvs.pass": "",
	"mvs.hlq": "IBMUSER", "mvs.deps_hlq": "", "mvs.deps_volume": "",
	"jes.jobclass": "A", "jes.msgclass": "H", "build.id": "",
}

var envMap = map[string]string{
	"mvs.host": "MBT_MVS_HOST", "mvs.port": "MBT_MVS_PORT", "mvs.user": "MBT_MVS_USER",
	"mvs.pass": "MBT_MVS_PASS", "mvs.hlq": "MBT_MVS_HLQ", "mvs.deps_hlq": "MBT_MVS_DEPS_HLQ",
	"mvs.deps_volume": "MBT_MVS_DEPS_VOLUME", "jes.jobclass": "MBT_JES_JOBCLASS",
	"jes.msgclass": "MBT_JES_MSGCLASS", "build.id": "MBT_BUILD_ID",
}

// Config is the merged view.
type Config struct {
	dotenv map[string]string
	global map[string]any
	fixed  map[string]string // Fixed: the values of a target, nothing else
}

// Fixed is a configuration of given values (a target's); keys not given
// fall back to the defaults, and the environment, .env and config.toml are
// not read.
func Fixed(values map[string]string) *Config {
	return &Config{fixed: values}
}

// Load reads .env in root and ~/.mbt/config.toml.
func Load(root string) *Config {
	c := &Config{dotenv: map[string]string{}, global: map[string]any{}}
	if f, err := os.Open(filepath.Join(root, ".env")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			k, v, _ := strings.Cut(line, "=")
			c.dotenv[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
		f.Close()
	}
	if home, err := os.UserHomeDir(); err == nil {
		toml.DecodeFile(filepath.Join(home, ".mbt", "config.toml"), &c.global)
	}
	return c
}

// Outside reads what mbt 2 merged besides a project's .env: the environment
// and ~/.mbt/config.toml.  target import fills from it what the .env file
// leaves out, so the imported target is the one mbt 2 actually used.
func Outside() *Config {
	c := &Config{dotenv: map[string]string{}, global: map[string]any{}}
	if home, err := os.UserHomeDir(); err == nil {
		toml.DecodeFile(filepath.Join(home, ".mbt", "config.toml"), &c.global)
	}
	return c
}

// EnvName is the MBT_* variable of a dotted key ("mvs.hlq" -> MBT_MVS_HLQ).
func EnvName(key string) string { return envMap[key] }

// Get resolves a dotted key; Source says where it came from.
func (c *Config) Get(key string) string { v, _ := c.Source(key); return v }

// Source returns the value and its origin: env, .env, ~/.mbt/config.toml or
// default.
func (c *Config) Source(key string) (string, string) {
	if c.fixed != nil {
		if v, ok := c.fixed[key]; ok && v != "" {
			return v, "target"
		}
		return defaults[key], "default"
	}
	if e, ok := envMap[key]; ok {
		if v, ok := os.LookupEnv(e); ok {
			return v, "env"
		}
		if v, ok := c.dotenv[e]; ok {
			return v, ".env"
		}
	}
	var cur any = c.global
	found := true
	for _, part := range strings.Split(key, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			found = false
			break
		}
		if cur, ok = m[part]; !ok {
			found = false
			break
		}
	}
	if found && cur != nil {
		return fmt.Sprint(cur), "~/.mbt/config.toml"
	}
	return defaults[key], "default"
}

func (c *Config) Host() string     { return c.Get("mvs.host") }
func (c *Config) User() string     { return c.Get("mvs.user") }
func (c *Config) Pass() string     { return c.Get("mvs.pass") }
func (c *Config) HLQ() string      { return c.Get("mvs.hlq") }
func (c *Config) Volume() string   { return c.Get("mvs.deps_volume") }
func (c *Config) JobClass() string { return c.Get("jes.jobclass") }
func (c *Config) MsgClass() string { return c.Get("jes.msgclass") }

// Port is mvs.port as a number.
func (c *Config) Port() (int, error) {
	p, err := strconv.Atoi(c.Get("mvs.port"))
	if err != nil {
		return 0, fmt.Errorf("mvs.port %q is not a number", c.Get("mvs.port"))
	}
	return p, nil
}
