// Package target names the MVS systems mbt works with
// (internals/mbt-3-extensions.md §4).
//
// Targets live in ~/.mbt/targets.toml, one table per system:
//
//	[target.lab]
//	default  = true
//	hlq      = "IBMUSER"
//	volume   = "PUB001"            # where RECEIVE allocates
//	jobclass = "A"
//	msgclass = "H"
//	[target.lab.mvsmf]             # required
//	url      = "http://lab:1080"
//	user     = "IBMUSER"
//	password = { keychain = "mbt/lab" }
//	[target.lab.hercules]          # optional: the console fallback
//	[target.lab.ssh]               # optional: host, user, identity, port
//	[target.lab.tn3270]            # optional: host, port, tls, user, password
//	[target.lab.console]
//	order = ["mvsmf", "hercules"]
//
// A password is { env = "VAR" }, { keychain = "name" }, { cmd = ["prog",
// "arg"] } (argv, no shell), or a literal -- the last only in a file nobody
// else can read. In CI a target comes from the environment instead
// (MBT_TARGET_MVSMF_URL, ...); without either, the mbt 2 settings (MBT_MVS_*,
// .env, ~/.mbt/config.toml) still work, with a warning.
package target

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// Error is a target that is wrong or cannot be had (exit 2).
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func errf(format string, a ...any) error { return &Error{fmt.Sprintf(format, a...)} }

// Secret is a password and where it comes from; Resolve reads it.
type Secret struct {
	Literal  string
	Env      string
	Keychain string
	Cmd      []string
	set      bool
}

// Set says whether a password was given at all.
func (s Secret) Set() bool { return s.set }

// Source says where the password comes from, never what it is.
func (s Secret) Source() string {
	switch {
	case s.Env != "":
		return "env " + s.Env
	case s.Keychain != "":
		return "keychain " + s.Keychain
	case len(s.Cmd) > 0:
		return "cmd " + s.Cmd[0]
	case s.set:
		return "in the file"
	}
	return "none"
}

// Resolve returns the password. One that a source (environment, keychain,
// command) yields empty is an error naming that source: an entry stored from
// a non-interactive shell comes out empty without a word, and the logon that
// follows fails with a 401 that does not say why.
func (s Secret) Resolve() (string, error) {
	v, err := s.resolve()
	if err == nil && v == "" && (s.Env != "" || s.Keychain != "" || len(s.Cmd) > 0) {
		return "", errf("password: %s is empty", s.Source())
	}
	return v, err
}

func (s Secret) resolve() (string, error) {
	switch {
	case s.Env != "":
		v, ok := os.LookupEnv(s.Env)
		if !ok {
			return "", errf("password: environment variable %s is not set", s.Env)
		}
		return v, nil
	case s.Keychain != "":
		var cmd *exec.Cmd
		if runtime.GOOS == "darwin" {
			cmd = exec.Command("security", "find-generic-password", "-s", s.Keychain, "-w")
		} else {
			cmd = exec.Command("secret-tool", "lookup", "service", s.Keychain)
		}
		out, err := cmd.Output()
		if err != nil {
			return "", errf("password: keychain entry %q not readable (%v)", s.Keychain, err)
		}
		return strings.TrimRight(string(out), "\r\n"), nil
	case len(s.Cmd) > 0:
		var errb bytes.Buffer
		cmd := exec.Command(s.Cmd[0], s.Cmd[1:]...)
		cmd.Stderr = &errb
		out, err := cmd.Output()
		if err != nil {
			return "", errf("password: %s failed (%v): %s", s.Cmd[0], err, strings.TrimSpace(errb.String()))
		}
		line, _, _ := strings.Cut(string(out), "\n")
		return strings.TrimRight(line, "\r"), nil
	}
	return s.Literal, nil
}

// Endpoint is an HTTP service: mvsMF or the Hercules web console.
type Endpoint struct {
	URL      string
	User     string
	Password Secret
}

// HostPort splits the URL for mbt's mvsMF client (http only).
func (e *Endpoint) HostPort() (string, int, error) {
	u, err := url.Parse(e.URL)
	if err != nil || u.Host == "" {
		return "", 0, errf("url %q: not a URL (http://host:port)", e.URL)
	}
	if u.Scheme != "http" {
		return "", 0, errf("url %q: only http is supported", e.URL)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "80"
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, errf("url %q: bad port", e.URL)
	}
	return host, n, nil
}

// SSH is the optional SSH access (keys or agent, no passwords).
type SSH struct {
	Host, User, Identity string
	Port                 int
}

// TN3270 is the optional terminal access for mbt test --tso.
type TN3270 struct {
	Host     string
	Port     int
	TLS      bool
	User     string
	Password Secret
}

// Addr is host:port.
func (t *TN3270) Addr() string { return net.JoinHostPort(t.Host, strconv.Itoa(t.Port)) }

// Target is one MVS system.
type Target struct {
	Name                            string
	Default                         bool
	HLQ, Volume, JobClass, MsgClass string
	MVSMF                           *Endpoint
	Hercules                        *Endpoint
	SSH                             *SSH
	TN3270                          *TN3270
	Console                         []string
	Source                          string // where it was defined
}

// File is ~/.mbt/targets.toml.
type File struct {
	Path    string
	Targets map[string]*Target
}

// Names lists the targets, sorted.
func (f *File) Names() []string {
	var n []string
	for k := range f.Targets {
		n = append(n, k)
	}
	sort.Strings(n)
	return n
}

// Path is the targets file under home (~/.mbt).
func Path(home string) string { return filepath.Join(home, "targets.toml") }

// Load reads home/targets.toml; a missing file is an empty one.
func Load(home string) (*File, error) {
	f := &File{Path: Path(home), Targets: map[string]*Target{}}
	data, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	raw := map[string]any{}
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return nil, errf("%s: %v", f.Path, err)
	}
	for k := range raw {
		if k != "target" {
			return nil, errf("%s: unknown key '%s' -- every system is a [target.NAME]", f.Path, k)
		}
	}
	tt, _ := raw["target"].(map[string]any)
	literal := false
	for name, v := range tt {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errf("%s: target.%s must be a table", f.Path, name)
		}
		t, lit, err := parse(name, m)
		if err != nil {
			return nil, errf("%s: %v", f.Path, err)
		}
		literal = literal || lit
		t.Source = f.Path
		f.Targets[name] = t
	}
	if literal {
		if st, err := os.Stat(f.Path); err == nil && st.Mode().Perm()&0o077 != 0 {
			return nil, errf("%s holds a password and others can read it (mode %o) -- chmod 600 %s, or use { env = ... } / { keychain = ... } / { cmd = [...] }", f.Path, st.Mode().Perm(), f.Path)
		}
	}
	var defaults []string
	for n, t := range f.Targets {
		if t.Default {
			defaults = append(defaults, n)
		}
	}
	if len(defaults) > 1 {
		sort.Strings(defaults)
		return nil, errf("%s: more than one default target (%s)", f.Path, strings.Join(defaults, ", "))
	}
	return f, nil
}

var knownKeys = map[string][]string{
	"":         {"default", "hlq", "volume", "jobclass", "msgclass", "mvsmf", "hercules", "ssh", "tn3270", "console"},
	"mvsmf":    {"url", "user", "password"},
	"hercules": {"url", "user", "password"},
	"ssh":      {"host", "user", "identity", "port"},
	"tn3270":   {"host", "port", "tls", "user", "password"},
	"console":  {"order"},
}

func check(m map[string]any, section, where string) error {
	ok := map[string]bool{}
	for _, k := range knownKeys[section] {
		ok[k] = true
	}
	var bad []string
	for k := range m {
		if !ok[k] {
			bad = append(bad, k)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("%s: unknown key '%s'", where, bad[0])
	}
	return nil
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func parse(name string, m map[string]any) (*Target, bool, error) {
	where := "[target." + name + "]"
	if err := check(m, "", where); err != nil {
		return nil, false, err
	}
	t := &Target{Name: name, HLQ: str(m, "hlq"), Volume: str(m, "volume"), JobClass: str(m, "jobclass"), MsgClass: str(m, "msgclass")}
	t.Default, _ = m["default"].(bool)
	literal := false
	endpoint := func(k string) (*Endpoint, error) {
		s, ok := m[k].(map[string]any)
		if !ok {
			return nil, nil
		}
		w := fmt.Sprintf("[target.%s.%s]", name, k)
		if err := check(s, k, w); err != nil {
			return nil, err
		}
		e := &Endpoint{URL: str(s, "url"), User: str(s, "user")}
		pw, lit, err := secret(s["password"], w)
		if err != nil {
			return nil, err
		}
		literal = literal || lit
		e.Password = pw
		if e.URL == "" {
			return nil, fmt.Errorf("%s: url is required", w)
		}
		return e, nil
	}
	var err error
	if t.MVSMF, err = endpoint("mvsmf"); err != nil {
		return nil, false, err
	}
	if t.MVSMF == nil {
		return nil, false, fmt.Errorf("%s: [target.%s.mvsmf] is required -- mbt works through mvsMF", where, name)
	}
	if t.MVSMF.User == "" {
		return nil, false, fmt.Errorf("[target.%s.mvsmf]: user is required", name)
	}
	if t.Hercules, err = endpoint("hercules"); err != nil {
		return nil, false, err
	}
	if s, ok := m["ssh"].(map[string]any); ok {
		w := fmt.Sprintf("[target.%s.ssh]", name)
		if err := check(s, "ssh", w); err != nil {
			return nil, false, err
		}
		t.SSH = &SSH{Host: str(s, "host"), User: str(s, "user"), Identity: str(s, "identity"), Port: 22}
		if p, ok := s["port"].(int64); ok {
			t.SSH.Port = int(p)
		}
		if t.SSH.Host == "" {
			return nil, false, fmt.Errorf("%s: host is required", w)
		}
	}
	if s, ok := m["tn3270"].(map[string]any); ok {
		w := fmt.Sprintf("[target.%s.tn3270]", name)
		if err := check(s, "tn3270", w); err != nil {
			return nil, false, err
		}
		t.TN3270 = &TN3270{Host: str(s, "host"), User: str(s, "user"), Port: 3270}
		if p, ok := s["port"].(int64); ok {
			t.TN3270.Port = int(p)
		}
		t.TN3270.TLS, _ = s["tls"].(bool)
		pw, lit, err := secret(s["password"], w)
		if err != nil {
			return nil, false, err
		}
		literal = literal || lit
		t.TN3270.Password = pw
		if t.TN3270.Host == "" {
			return nil, false, fmt.Errorf("%s: host is required", w)
		}
	}
	t.Console = []string{"mvsmf"}
	if t.Hercules != nil {
		t.Console = []string{"mvsmf", "hercules"}
	}
	if s, ok := m["console"].(map[string]any); ok {
		w := fmt.Sprintf("[target.%s.console]", name)
		if err := check(s, "console", w); err != nil {
			return nil, false, err
		}
		if l, ok := s["order"].([]any); ok {
			t.Console = nil
			for _, x := range l {
				c, _ := x.(string)
				switch {
				case c == "mvsmf":
				case c == "hercules" && t.Hercules != nil:
				case c == "hercules":
					return nil, false, fmt.Errorf("%s: order names hercules, but there is no [target.%s.hercules]", w, name)
				default:
					return nil, false, fmt.Errorf("%s: order: %q is not a console channel (mvsmf, hercules)", w, c)
				}
				t.Console = append(t.Console, c)
			}
		}
	}
	return t, literal, nil
}

func secret(v any, where string) (Secret, bool, error) {
	switch x := v.(type) {
	case nil:
		return Secret{}, false, nil
	case string:
		return Secret{Literal: x, set: true}, true, nil
	case map[string]any:
		s := Secret{set: true}
		n := 0
		for k, val := range x {
			n++
			switch k {
			case "env":
				s.Env, _ = val.(string)
			case "keychain":
				s.Keychain, _ = val.(string)
			case "cmd":
				l, ok := val.([]any)
				if !ok || len(l) == 0 {
					return Secret{}, false, fmt.Errorf("%s: password cmd must be an argv list: { cmd = [\"pass\", \"show\", \"x\"] }", where)
				}
				for _, a := range l {
					sa, _ := a.(string)
					s.Cmd = append(s.Cmd, sa)
				}
			default:
				return Secret{}, false, fmt.Errorf("%s: password { %s = ... }: use env, keychain or cmd", where, k)
			}
		}
		if n != 1 {
			return Secret{}, false, fmt.Errorf("%s: password: exactly one of env, keychain, cmd", where)
		}
		return s, false, nil
	}
	return Secret{}, false, fmt.Errorf("%s: password must be a string or { env | keychain | cmd = ... }", where)
}

// FromEnv builds the target CI sets through the environment; ok is false
// when MBT_TARGET_MVSMF_URL is not set.
func FromEnv(get func(string) string) (*Target, bool) {
	u := get("MBT_TARGET_MVSMF_URL")
	if u == "" {
		return nil, false
	}
	t := &Target{Name: "env", Source: "environment (MBT_TARGET_*)",
		HLQ: get("MBT_TARGET_HLQ"), Volume: get("MBT_TARGET_VOLUME"), JobClass: get("MBT_TARGET_JOBCLASS"), MsgClass: get("MBT_TARGET_MSGCLASS"),
		MVSMF: &Endpoint{URL: u, User: get("MBT_TARGET_MVSMF_USER"), Password: Secret{Env: "MBT_TARGET_MVSMF_PASSWORD", set: true}},
	}
	t.Console = []string{"mvsmf"}
	if h := get("MBT_TARGET_HERCULES_URL"); h != "" {
		t.Hercules = &Endpoint{URL: h, User: get("MBT_TARGET_HERCULES_USER")}
		if get("MBT_TARGET_HERCULES_PASSWORD") != "" {
			t.Hercules.Password = Secret{Env: "MBT_TARGET_HERCULES_PASSWORD", set: true}
		}
		t.Console = append(t.Console, "hercules")
	}
	if h := get("MBT_TARGET_TN3270_HOST"); h != "" {
		t.TN3270 = &TN3270{Host: h, Port: 3270, User: get("MBT_TARGET_TN3270_USER")}
		if p, err := strconv.Atoi(get("MBT_TARGET_TN3270_PORT")); err == nil {
			t.TN3270.Port = p
		}
		if get("MBT_TARGET_TN3270_PASSWORD") != "" {
			t.TN3270.Password = Secret{Env: "MBT_TARGET_TN3270_PASSWORD", set: true}
		}
	}
	return t, true
}

// Legacy is what mbt 2 read: MBT_MVS_* from the environment, .env and
// ~/.mbt/config.toml. get resolves a dotted mbt 2 key ("mvs.host").
func Legacy(get func(string) string) *Target {
	return &Target{Name: "legacy", Source: "mbt 2 settings (MBT_MVS_*, .env, ~/.mbt/config.toml)",
		HLQ: get("mvs.hlq"), Volume: get("mvs.deps_volume"), JobClass: get("jes.jobclass"), MsgClass: get("jes.msgclass"),
		MVSMF: &Endpoint{URL: "http://" + net.JoinHostPort(get("mvs.host"), get("mvs.port")), User: get("mvs.user"),
			Password: Secret{Literal: get("mvs.pass"), set: true}},
		Console: []string{"mvsmf"},
	}
}

// Select picks the target: an explicit name (--target), else MBT_TARGET,
// else the environment's, else the default (or the only one) in the file,
// else the mbt 2 settings. warn is set when the choice deserves a word.
func Select(name string, f *File, get func(string) string, legacy func() *Target) (t *Target, warn string, err error) {
	if name == "" {
		name = get("MBT_TARGET")
	}
	if name != "" {
		if name == "env" {
			if t, ok := FromEnv(get); ok {
				return t, "", nil
			}
			return nil, "", errf("target env: MBT_TARGET_MVSMF_URL is not set")
		}
		if t, ok := f.Targets[name]; ok {
			return t, "", nil
		}
		if len(f.Targets) == 0 {
			return nil, "", errf("no target %q: %s does not exist or defines none", name, f.Path)
		}
		return nil, "", errf("no target %q in %s (defined: %s)", name, f.Path, strings.Join(f.Names(), ", "))
	}
	if t, ok := FromEnv(get); ok {
		return t, "", nil
	}
	for _, t := range f.Targets {
		if t.Default {
			return t, "", nil
		}
	}
	if len(f.Targets) == 1 {
		for _, t := range f.Targets {
			return t, "", nil
		}
	}
	if len(f.Targets) > 1 {
		return nil, "", errf("%s defines %d targets and none is default -- say --target NAME, or set default = true on one", f.Path, len(f.Targets))
	}
	return legacy(), fmt.Sprintf("no %s: using the mbt 2 settings (MBT_MVS_*, .env) -- 'mbt target import .env --name NAME' turns them into a target", f.Path), nil
}

// Defaults fills what a target may leave out.
func (t *Target) Defaults() {
	if t.HLQ == "" && t.MVSMF != nil {
		t.HLQ = strings.ToUpper(t.MVSMF.User)
	}
	if t.JobClass == "" {
		t.JobClass = "A"
	}
	if t.MsgClass == "" {
		t.MsgClass = "H"
	}
}
