package target

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, mode os.FileMode, body string) string {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "targets.toml"), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	os.Chmod(filepath.Join(home, "targets.toml"), mode)
	return home
}

const two = `
[target.lab]
default = true
hlq = "MIKE"
volume = "PUB001"
[target.lab.mvsmf]
url = "http://lab:1080"
user = "MIKE"
password = { env = "LAB_PASS" }
[target.lab.hercules]
url = "http://lab:8038"
[target.lab.tn3270]
host = "lab"
user = "TSOTEST"
password = { cmd = ["printf", "secret\nignored"] }
[target.lab.ssh]
host = "lab"

[target.ci]
[target.ci.mvsmf]
url = "http://localhost:8080"
user = "IBMUSER"
password = "sys1"
[target.ci.console]
order = ["mvsmf"]
`

func noEnv(string) string { return "" }

func legacyNever(t *testing.T) func() *Target {
	return func() *Target { t.Fatal("fell back to the mbt 2 settings"); return nil }
}

func TestLoadAndSelect(t *testing.T) {
	home := write(t, 0o600, two)
	f, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	lab := f.Targets["lab"]
	if !lab.Default || lab.HLQ != "MIKE" || lab.Volume != "PUB001" || lab.TN3270.Port != 3270 || lab.SSH.Port != 22 {
		t.Errorf("lab: %+v", lab)
	}
	if strings.Join(lab.Console, ",") != "mvsmf,hercules" || strings.Join(f.Targets["ci"].Console, ",") != "mvsmf" {
		t.Errorf("console order: %v %v", lab.Console, f.Targets["ci"].Console)
	}
	h, p, err := lab.MVSMF.HostPort()
	if h != "lab" || p != 1080 || err != nil {
		t.Errorf("hostport %s %d %v", h, p, err)
	}

	// selection: explicit, MBT_TARGET, default
	for _, c := range []struct{ name, envTarget, want string }{{"ci", "", "ci"}, {"", "ci", "ci"}, {"", "", "lab"}} {
		get := func(k string) string {
			if k == "MBT_TARGET" {
				return c.envTarget
			}
			return ""
		}
		got, warn, err := Select(c.name, f, get, legacyNever(t))
		if err != nil || got.Name != c.want || warn != "" {
			t.Errorf("select %q/%q: %v %q %v", c.name, c.envTarget, got, warn, err)
		}
	}
	if _, _, err := Select("nope", f, noEnv, legacyNever(t)); err == nil || !strings.Contains(err.Error(), "defined: ci, lab") {
		t.Errorf("unknown: %v", err)
	}
}

func TestSecrets(t *testing.T) {
	f, err := Load(write(t, 0o600, two))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAB_PASS", "from-env")
	if pw, err := f.Targets["lab"].MVSMF.Password.Resolve(); pw != "from-env" || err != nil {
		t.Errorf("env: %q %v", pw, err)
	}
	if pw, err := f.Targets["lab"].TN3270.Password.Resolve(); pw != "secret" || err != nil {
		t.Errorf("cmd: %q %v", pw, err)
	}
	if pw, _ := f.Targets["ci"].MVSMF.Password.Resolve(); pw != "sys1" {
		t.Errorf("literal: %q", pw)
	}
	if s := f.Targets["lab"].MVSMF.Password.Source(); s != "env LAB_PASS" || strings.Contains(f.Targets["ci"].MVSMF.Password.Source(), "sys1") {
		t.Errorf("source: %q", s)
	}
	os.Unsetenv("LAB_PASS")
	if _, err := f.Targets["lab"].MVSMF.Password.Resolve(); err == nil {
		t.Error("an unset env password resolved")
	}
}

func TestLiteralPasswordNeedsPrivateFile(t *testing.T) {
	if _, err := Load(write(t, 0o644, two)); err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("readable file with a password: %v", err)
	}
	noLiteral := strings.Replace(two, `password = "sys1"`, `password = { env = "X" }`, 1)
	if _, err := Load(write(t, 0o644, noLiteral)); err != nil {
		t.Errorf("readable file without a literal password: %v", err)
	}
}

func TestLoadRefuses(t *testing.T) {
	for want, body := range map[string]string{
		"is required -- mbt works through mvsMF": "[target.x]\nhlq = \"A\"\n",
		"unknown key 'pasword'":                  "[target.x.mvsmf]\nurl = \"http://a:1\"\nuser = \"U\"\npasword = \"x\"\n",
		"exactly one of env, keychain, cmd":      "[target.x.mvsmf]\nurl = \"http://a:1\"\nuser = \"U\"\npassword = { env = \"A\", keychain = \"b\" }\n",
		"order names hercules":                   "[target.x.mvsmf]\nurl = \"http://a:1\"\nuser = \"U\"\n[target.x.console]\norder = [\"hercules\"]\n",
		"more than one default":                  "[target.a]\ndefault = true\n[target.a.mvsmf]\nurl = \"http://a:1\"\nuser = \"U\"\n[target.b]\ndefault = true\n[target.b.mvsmf]\nurl = \"http://b:1\"\nuser = \"U\"\n",
		"every system is a [target.NAME]":        "[mvsdev]\nhost = \"x\"\n",
	} {
		if _, err := Load(write(t, 0o600, body)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
}

func TestEnvAndLegacy(t *testing.T) {
	env := map[string]string{"MBT_TARGET_MVSMF_URL": "http://localhost:8080", "MBT_TARGET_MVSMF_USER": "IBMUSER",
		"MBT_TARGET_HLQ": "IBMUSER", "MBT_TARGET_VOLUME": "PUB000", "MBT_TARGET_TN3270_HOST": "localhost", "MBT_TARGET_TN3270_PORT": "3270"}
	get := func(k string) string { return env[k] }
	f, _ := Load(t.TempDir())
	got, _, err := Select("", f, get, legacyNever(t))
	if err != nil || got.Name != "env" || got.Volume != "PUB000" || got.TN3270 == nil || got.MVSMF.Password.Env != "MBT_TARGET_MVSMF_PASSWORD" {
		t.Errorf("env target: %+v %v", got, err)
	}
	// nothing at all: the mbt 2 settings, with a warning
	legacy := map[string]string{"mvs.host": "mvsdev.lan", "mvs.port": "1080", "mvs.user": "IBMUSER", "mvs.pass": "x", "mvs.hlq": "IBMUSER", "mvs.deps_volume": "PUB001"}
	got, warn, err := Select("", f, noEnv, func() *Target { return Legacy(func(k string) string { return legacy[k] }) })
	if err != nil || got.MVSMF.URL != "http://mvsdev.lan:1080" || got.Volume != "PUB001" || !strings.Contains(warn, "mbt target import") {
		t.Errorf("legacy: %+v %q %v", got, warn, err)
	}
}

func TestImport(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"MBT_MVS_HOST": "mvsdev.lan", "MBT_MVS_PORT": "1080", "MBT_MVS_USER": "IBMUSER", "MBT_MVS_PASS": "sys1",
		"MBT_MVS_HLQ": "IBMUSER", "MBT_MVS_DEPS_VOLUME": "PUB001", "MBT_MVS_DEPS_HLQ": "OLD", "MBT_BUILD_ID": "1"}
	shown, skipped, err := Import(home, "mvsdev", env)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(shown, "sys1") || len(skipped) != 2 {
		t.Errorf("shown:\n%s\nskipped %v", shown, skipped)
	}
	st, _ := os.Stat(Path(home))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", st.Mode().Perm())
	}
	f, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	m := f.Targets["mvsdev"]
	if !m.Default || m.MVSMF.URL != "http://mvsdev.lan:1080" || m.Volume != "PUB001" {
		t.Errorf("%+v", m)
	}
	if _, _, err := Import(home, "mvsdev", env); err == nil {
		t.Error("an existing target was replaced")
	}
	if _, _, err := Import(home, "lab", env); err != nil {
		t.Fatal(err)
	}
	if f, _ = Load(home); f.Targets["lab"].Default {
		t.Error("a second import became default too")
	}
}

func TestPing(t *testing.T) {
	// mvsMF answers 401 without credentials: that is "answering"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	tg := &Target{Name: "x", MVSMF: &Endpoint{URL: srv.URL}, Hercules: &Endpoint{URL: "http://127.0.0.1:1"},
		TN3270: &TN3270{Host: "127.0.0.1", Port: port}}
	ps := Ping(tg, 2*time.Second)
	if !ps[0].OK || !strings.Contains(ps[0].Detail, "HTTP 401") || ps[1].OK || !ps[2].OK {
		t.Errorf("%+v", ps)
	}
	p, _ := Wait(&Target{MVSMF: &Endpoint{URL: "http://127.0.0.1:1"}}, 0, func(time.Duration) {})
	if p.OK {
		t.Error("waited for nothing and found it")
	}
}
