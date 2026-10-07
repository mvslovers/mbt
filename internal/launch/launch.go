// Package launch runs the mbt a project asks for (design §5).
//
// Every mbt binary is launcher and core at once, the way the go command
// switches toolchains: a project pins `[toolchain] mbt = "3.0"` (any 3.0.x)
// or `"3.0.2"` (exactly that); when the running mbt does not match, the
// highest matching version installed under ~/.mbt/versions/ runs instead, and
// when none is installed the newest matching release of mvslovers/mbt is
// downloaded there first -- checked against the release's SHA256 list.
//
// MBT_VERSION_SWITCHED is set for the child, so a switched mbt never switches
// again; MBT_NO_SWITCH=1 keeps the running mbt (a development build).
package launch

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/mvslovers/mbt/internal/version"

	"github.com/mvslovers/mbt/internal/deps"
)

// Options for Switch; the zero value means the real thing.
type Options struct {
	Root    string // project directory (cwd)
	Home    string // ~/.mbt
	API     string // https://api.github.com
	Repo    string // mvslovers/mbt
	Log     func(string)
	GOOS    string
	GOARCH  string
	Run     func(bin string, args []string, env []string) int // exec the chosen mbt
	Timeout time.Duration
}

func (o *Options) fill() {
	if o.Root == "" {
		o.Root, _ = os.Getwd()
	}
	if o.Home == "" {
		if h := os.Getenv("MBT_HOME"); h != "" {
			o.Home = h
		} else {
			home, _ := os.UserHomeDir()
			o.Home = filepath.Join(home, ".mbt")
		}
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Repo == "" {
		o.Repo = "mvslovers/mbt"
	}
	if o.Log == nil {
		o.Log = func(s string) { fmt.Fprintf(os.Stderr, "[mbt] %s\n", s) }
	}
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.GOARCH == "" {
		o.GOARCH = runtime.GOARCH
	}
	if o.Run == nil {
		o.Run = run
	}
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
}

func run(bin string, args []string, env []string) int {
	cmd := exec.Command(bin, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "[mbt] ERROR: cannot run %s: %v\n", bin, err)
		return 99
	}
	return 0
}

// Pin reads [toolchain] mbt from root/mbt.toml ("" when there is none).
func Pin(root string) (string, error) {
	var f struct {
		Toolchain struct {
			MBT string `toml:"mbt"`
		} `toml:"toolchain"`
	}
	p := filepath.Join(root, "mbt.toml")
	if _, err := os.Stat(p); err != nil {
		return "", nil
	}
	if _, err := toml.DecodeFile(p, &f); err != nil {
		return "", nil // the core reports a broken file properly
	}
	pin := strings.TrimSpace(f.Toolchain.MBT)
	if pin != "" && !pinRE.MatchString(pin) {
		return "", fmt.Errorf("[toolchain] mbt = %q: write a version, \"3.0\" (any 3.0.x) or \"3.0.2\" (exactly)", pin)
	}
	return pin, nil
}

var pinRE = regexp.MustCompile(`^\d+\.\d+(\.\d+(-[0-9A-Za-z.]+)?)?$`)

// Matches says whether version v satisfies pin: "3.0" is any 3.0.x
// (prereleases included), "3.0.2" and "3.0.2-dev" are exact.
func Matches(pin, v string) bool {
	if strings.Count(pin, ".") == 1 {
		pv, err := version.Parse(v)
		if err != nil {
			return false
		}
		return fmt.Sprintf("%d.%d", pv.Major, pv.Minor) == pin
	}
	return pin == v
}

// Switch runs the pinned mbt when the running one (self) does not match.
// It returns handled=false when the running mbt should carry on.
func Switch(self string, args []string, o Options) (handled bool, code int) {
	if os.Getenv("MBT_VERSION_SWITCHED") != "" || os.Getenv("MBT_NO_SWITCH") == "1" {
		return false, 0
	}
	o.fill()
	pin, err := Pin(o.Root)
	if err != nil {
		o.Log("ERROR: " + err.Error())
		return true, 2
	}
	if pin == "" || Matches(pin, self) {
		return false, 0
	}
	bin, ver, err := installed(pin, o)
	if err == nil && bin == "" {
		bin, ver, err = download(pin, o)
	}
	if err != nil {
		o.Log(fmt.Sprintf("ERROR: mbt.toml asks for mbt %s, this is %s, and %v", pin, self, err))
		return true, 3
	}
	env := append(os.Environ(), "MBT_VERSION_SWITCHED="+ver)
	return true, o.Run(bin, args, env)
}

func exe(goos string) string {
	if goos == "windows" {
		return "mbt.exe"
	}
	return "mbt"
}

// installed finds the highest version under Home/versions matching pin.
func installed(pin string, o Options) (bin, ver string, err error) {
	dirs, _ := os.ReadDir(filepath.Join(o.Home, "versions"))
	var vs []version.Version
	for _, d := range dirs {
		if !d.IsDir() || !Matches(pin, d.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(o.Home, "versions", d.Name(), exe(o.GOOS))); err != nil {
			continue
		}
		if v, err := version.Parse(d.Name()); err == nil {
			vs = append(vs, v)
		}
	}
	if len(vs) == 0 {
		return "", "", nil
	}
	sort.Slice(vs, func(i, j int) bool { return version.Compare(vs[i], vs[j]) > 0 })
	ver = vs[0].String()
	return filepath.Join(o.Home, "versions", ver, exe(o.GOOS)), ver, nil
}

type release struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func get(url string, o Options) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" && strings.HasPrefix(url, o.API) {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: o.Timeout}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: %s", url, deps.HTTPStatus(resp))
	}
	return io.ReadAll(resp.Body)
}

// AssetName is the archive of one platform: mbt-3.0.1-darwin-arm64.tar.gz.
func AssetName(ver, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("mbt-%s-%s-%s%s", ver, goos, goarch, ext)
}

// SumsName is the release's SHA256 list.
func SumsName(ver string) string { return fmt.Sprintf("mbt-%s-sha256.txt", ver) }

func download(pin string, o Options) (bin, ver string, err error) {
	data, err := get(fmt.Sprintf("%s/repos/%s/releases?per_page=100", o.API, o.Repo), o)
	if err != nil {
		return "", "", fmt.Errorf("no matching mbt is installed and the releases cannot be listed: %v", err)
	}
	var rels []release
	if err := json.Unmarshal(data, &rels); err != nil {
		return "", "", fmt.Errorf("release list: %v", err)
	}
	// newest matching; a stable release wins over a prerelease of any version
	var best *release
	var bestV version.Version
	for i := range rels {
		r := &rels[i]
		v, err := version.Parse(strings.TrimPrefix(r.Tag, "v"))
		if r.Draft || err != nil || !Matches(pin, v.String()) {
			continue
		}
		if best == nil || (!v.IsPre() && bestV.IsPre()) || (v.IsPre() == bestV.IsPre() && version.Compare(v, bestV) > 0) {
			best, bestV = r, v
		}
	}
	if best == nil {
		return "", "", fmt.Errorf("no release of %s matches %s", o.Repo, pin)
	}
	ver = bestV.String()
	want, sums := AssetName(ver, o.GOOS, o.GOARCH), SumsName(ver)
	urls := map[string]string{}
	for _, a := range best.Assets {
		urls[a.Name] = a.URL
	}
	if urls[want] == "" || urls[sums] == "" {
		return "", "", fmt.Errorf("release %s has no %s (or no %s)", best.Tag, want, sums)
	}
	o.Log(fmt.Sprintf("mbt.toml asks for mbt %s: downloading %s", pin, want))
	archive, err := get(urls[want], o)
	if err != nil {
		return "", "", err
	}
	list, err := get(urls[sums], o)
	if err != nil {
		return "", "", err
	}
	if err := verify(archive, want, list); err != nil {
		return "", "", err
	}
	dir := filepath.Join(o.Home, "versions", ver)
	tmp := dir + ".partial"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return "", "", err
	}
	if err := extract(archive, want, exe(o.GOOS), filepath.Join(tmp, exe(o.GOOS))); err != nil {
		os.RemoveAll(tmp)
		return "", "", err
	}
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", "", err
	}
	o.Log(fmt.Sprintf("Installed mbt %s in %s", ver, dir))
	return filepath.Join(dir, exe(o.GOOS)), ver, nil
}

func verify(data []byte, name string, list []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if f[0] != got {
				return fmt.Errorf("%s: SHA256 %s, the release lists %s -- not installed", name, got, f[0])
			}
			return nil
		}
	}
	return fmt.Errorf("%s is not in the release's SHA256 list -- not installed", name)
}

// extract writes the one member called member (at any depth) to dest.
func extract(data []byte, name, member, dest string) error {
	var r io.Reader
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, f := range z.File {
			if filepath.Base(f.Name) == member {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				defer rc.Close()
				r = rc
				break
			}
		}
	} else {
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == member {
				r = tr
				break
			}
		}
	}
	if r == nil {
		return fmt.Errorf("%s holds no %s", name, member)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
