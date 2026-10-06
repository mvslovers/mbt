package target

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ReadDotenv reads KEY=VALUE lines (# comments, quotes stripped).
func ReadDotenv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		k, v, _ := strings.Cut(line, "=")
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return env, sc.Err()
}

// Import adds a target built from an mbt 2 .env to home/targets.toml and
// returns what it wrote, the password replaced by its source. Only the
// variables mbt 3 still reads come along: MBT_MVS_HOST/PORT/USER/PASS/HLQ,
// MBT_MVS_DEPS_VOLUME (as volume), MBT_JES_JOBCLASS/MSGCLASS. The file is
// created readable by its owner only; an existing target is never replaced.
func Import(home, name string, env map[string]string) (written string, skipped []string, err error) {
	if !nameRE.MatchString(name) {
		return "", nil, errf("target name %q: lower case letters, digits, - and _", name)
	}
	f, err := Load(home)
	if err != nil {
		return "", nil, err
	}
	if _, dup := f.Targets[name]; dup {
		return "", nil, errf("%s has a target %q already -- choose another name", f.Path, name)
	}
	host := env["MBT_MVS_HOST"]
	if host == "" {
		return "", nil, errf("no MBT_MVS_HOST -- nothing to import")
	}
	port := env["MBT_MVS_PORT"]
	if port == "" {
		port = "1080"
	}
	for _, k := range []string{"MBT_MVS_DEPS_HLQ", "MBT_BUILD_ID"} {
		if _, ok := env[k]; ok {
			skipped = append(skipped, k+" (no longer read)")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n[target.%s]\n", name)
	if len(f.Targets) == 0 {
		b.WriteString("default  = true\n")
	}
	for _, kv := range [][2]string{{"hlq", "MBT_MVS_HLQ"}, {"volume", "MBT_MVS_DEPS_VOLUME"}, {"jobclass", "MBT_JES_JOBCLASS"}, {"msgclass", "MBT_JES_MSGCLASS"}} {
		if v := env[kv[1]]; v != "" {
			fmt.Fprintf(&b, "%-8s = %q\n", kv[0], v)
		}
	}
	fmt.Fprintf(&b, "\n[target.%s.mvsmf]\nurl      = %q\nuser     = %q\n", name, "http://"+host+":"+port, env["MBT_MVS_USER"])
	shown := b.String()
	if pw := env["MBT_MVS_PASS"]; pw != "" {
		fmt.Fprintf(&b, "password = %q\n", pw)
		shown += "password = \"…\"   # from MBT_MVS_PASS; consider { keychain = ... } or { env = ... }\n"
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return "", nil, err
	}
	path := Path(home)
	fh, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	if _, err := fh.WriteString(b.String()); err != nil {
		fh.Close()
		return "", nil, err
	}
	if err := fh.Close(); err != nil {
		return "", nil, err
	}
	// an existing file that others could read must not now hold a password
	if st, err := os.Stat(path); err == nil && st.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(path, 0o600); err != nil {
			return "", nil, err
		}
	}
	return shown, skipped, nil
}

// Home is ~/.mbt, or MBT_HOME.
func Home() string {
	if h := os.Getenv("MBT_HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".mbt")
}
