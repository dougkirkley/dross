package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	yaml "go.yaml.in/yaml/v4"
)

// Home is the dross plugin root (~/.dross). Marketplaces are cloned under
// marketplaces/, installed plugins under plugins/, and state in plugins/state.json.
func Home() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".dross")
	}
	return ".dross"
}

func marketplacesDir() string { return filepath.Join(Home(), "marketplaces") }
func pluginsDir() string      { return filepath.Join(Home(), "plugins") }
func statePath() string       { return filepath.Join(pluginsDir(), "state.json") }

// State is the persisted set of added marketplaces and installed plugins.
type State struct {
	Marketplaces []MarketplaceRef  `json:"marketplaces"`
	Plugins      []InstalledPlugin `json:"plugins"`
}

// MarketplaceRef records an added marketplace and its local clone.
type MarketplaceRef struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Dir    string `json:"dir"`
}

// InstalledPlugin records an installed plugin's location and enabled state.
type InstalledPlugin struct {
	Name        string `json:"name"`
	Marketplace string `json:"marketplace,omitempty"`
	Dir         string `json:"dir"`
	Enabled     bool   `json:"enabled"`
}

// LoadState reads the plugin state, returning an empty state if none exists.
func LoadState() (*State, error) {
	b, err := os.ReadFile(statePath())
	if os.IsNotExist(err) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveState(s *State) error {
	if err := os.MkdirAll(pluginsDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), b, 0o644)
}

// --- marketplace.json ------------------------------------------------------

// Marketplace is a parsed .claude-plugin/marketplace.json index.
type Marketplace struct {
	Name    string              `yaml:"name" json:"name"`
	Owner   Owner               `yaml:"owner" json:"owner"`
	Plugins []MarketplacePlugin `yaml:"plugins" json:"plugins"`
	dir     string              // local clone dir
}

// Owner is a marketplace or plugin author.
type Owner struct {
	Name string `yaml:"name" json:"name"`
	URL  string `yaml:"url" json:"url,omitempty"`
}

// MarketplacePlugin is one entry in a marketplace's plugins array.
type MarketplacePlugin struct {
	Name        string       `yaml:"name" json:"name"`
	Source      PluginSource `yaml:"source" json:"source"`
	Description string       `yaml:"description" json:"description,omitempty"`
}

// PluginSource is a plugin's location: a relative path (string) or an object
// ({source:"github",repo:...} / {source:"url",url:...}).
type PluginSource struct {
	Path   string // relative path within the marketplace (string form)
	Type   string // "github" | "url" | "git-subdir" | "npm"
	Repo   string // owner/repo for github
	URL    string // git url
	SubDir string // path within repo for git-subdir
	Ref    string // branch/tag
}

func (p *PluginSource) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err == nil {
		p.Path = s
		return nil
	}
	var o struct {
		Source string `yaml:"source"`
		Repo   string `yaml:"repo"`
		URL    string `yaml:"url"`
		Path   string `yaml:"path"`
		Ref    string `yaml:"ref"`
	}
	if err := node.Decode(&o); err != nil {
		return err
	}
	p.Type, p.Repo, p.URL, p.SubDir, p.Ref = o.Source, o.Repo, o.URL, o.Path, o.Ref
	return nil
}

func readMarketplaceFile(dir string) (*Marketplace, error) {
	p := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	b, err := readCapped(p)
	if err != nil {
		return nil, fmt.Errorf("no marketplace.json in %s: %w", dir, err)
	}
	var m Marketplace
	if err := yaml.Unmarshal(b, &m); err != nil { // yaml.v4 parses JSON too
		return nil, err
	}
	m.dir = dir
	return &m, nil
}

// --- add / install ---------------------------------------------------------

// AddMarketplace clones a marketplace (github "owner/repo", a git URL, or a
// local path) into ~/.dross/marketplaces and records it.
func AddMarketplace(src string) (*Marketplace, error) {
	name := deriveName(src)
	dst := filepath.Join(marketplacesDir(), name)
	if err := materialize(src, dst); err != nil {
		return nil, err
	}
	m, err := readMarketplaceFile(dst)
	if err != nil {
		return nil, err
	}
	if m.Name != "" {
		name = m.Name
	}
	s, err := LoadState()
	if err != nil {
		return nil, err
	}
	s.Marketplaces = upsertMarketplace(s.Marketplaces, MarketplaceRef{Name: name, Source: src, Dir: dst})
	return m, saveState(s)
}

// Install materializes a plugin from an added marketplace into ~/.dross/plugins
// and records it. ref is "plugin" or "plugin@marketplace".
func Install(ref string) (*InstalledPlugin, error) {
	pluginName, mktName, _ := strings.Cut(ref, "@")
	s, err := LoadState()
	if err != nil {
		return nil, err
	}
	mp, entry, err := findPlugin(s, pluginName, mktName)
	if err != nil {
		return nil, err
	}
	if err := safeSegment(entry.Name); err != nil {
		return nil, fmt.Errorf("refusing to install plugin with unsafe name: %w", err)
	}
	dst := filepath.Join(pluginsDir(), entry.Name)
	if err := installSource(mp, entry.Source, dst); err != nil {
		return nil, err
	}
	ip := InstalledPlugin{Name: entry.Name, Marketplace: mp.Name, Dir: dst, Enabled: true}
	s.Plugins = upsertPlugin(s.Plugins, ip)
	return &ip, saveState(s)
}

// installSource resolves a plugin's source relative to its marketplace and
// materializes it into dst.
func installSource(mp *Marketplace, src PluginSource, dst string) error {
	switch {
	case src.Type == "github" && src.Repo != "":
		return materialize("https://github.com/"+src.Repo+".git", dst)
	case src.Type == "url" && src.URL != "":
		return materialize(src.URL, dst)
	case src.Path != "": // relative to the marketplace clone
		abs := filepath.Join(mp.dir, src.Path)
		rel, err := filepath.Rel(mp.dir, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("plugin source path escapes the marketplace: %q", src.Path)
		}
		return copyDir(abs, dst)
	default:
		return fmt.Errorf("unsupported plugin source (type %q)", src.Type)
	}
}

func findPlugin(s *State, plugin, mkt string) (*Marketplace, MarketplacePlugin, error) {
	for _, ref := range s.Marketplaces {
		if mkt != "" && ref.Name != mkt {
			continue
		}
		m, err := readMarketplaceFile(ref.Dir)
		if err != nil {
			continue
		}
		for _, p := range m.Plugins {
			if p.Name == plugin {
				return m, p, nil
			}
		}
	}
	return nil, MarketplacePlugin{}, fmt.Errorf("plugin %q not found in any added marketplace", plugin)
}

// Uninstall removes an installed plugin's files and state record.
func Uninstall(name string) error {
	s, err := LoadState()
	if err != nil {
		return err
	}
	out := s.Plugins[:0]
	for _, p := range s.Plugins {
		if p.Name == name {
			_ = os.RemoveAll(p.Dir)
			continue
		}
		out = append(out, p)
	}
	s.Plugins = out
	return saveState(s)
}

// SetEnabled toggles an installed plugin on or off.
func SetEnabled(name string, enabled bool) error {
	s, err := LoadState()
	if err != nil {
		return err
	}
	found := false
	for i := range s.Plugins {
		if s.Plugins[i].Name == name {
			s.Plugins[i].Enabled = enabled
			found = true
		}
	}
	if !found {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	return saveState(s)
}

// Listing is a plugin offered by an added marketplace, for the browse view.
type Listing struct {
	Name        string
	Marketplace string
	Description string
	Installed   bool
}

// Available returns every plugin offered across all added marketplaces, marking
// those already installed.
func Available() ([]Listing, error) {
	s, err := LoadState()
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	for _, p := range s.Plugins {
		installed[p.Name] = true
	}
	var out []Listing
	for _, ref := range s.Marketplaces {
		m, err := readMarketplaceFile(ref.Dir)
		if err != nil {
			continue
		}
		for _, p := range m.Plugins {
			out = append(out, Listing{
				Name: p.Name, Marketplace: m.Name,
				Description: p.Description, Installed: installed[p.Name],
			})
		}
	}
	return out, nil
}

// RemoveMarketplace forgets an added marketplace (its clone is deleted).
func RemoveMarketplace(name string) error {
	s, err := LoadState()
	if err != nil {
		return err
	}
	out := s.Marketplaces[:0]
	for _, m := range s.Marketplaces {
		if m.Name == name {
			_ = os.RemoveAll(m.Dir)
			continue
		}
		out = append(out, m)
	}
	s.Marketplaces = out
	return saveState(s)
}

// --- helpers ---------------------------------------------------------------

// materialize clones a git source into dst, or copies a local directory.
func materialize(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(dst)
	if isLocalPath(src) {
		return copyDir(src, dst)
	}
	url, ref := src, ""
	if i := strings.LastIndex(src, "@"); i > 0 && !strings.Contains(src[i:], "/") {
		url, ref = src[:i], src[i+1:]
	}
	// Bare "owner/repo" is GitHub shorthand. Anything with a scheme (https://,
	// ssh://, git://, file://) or an scp-style host (git@host:path) is a full git
	// URL and passes through to `git clone` unchanged — any git host works.
	if !strings.Contains(url, "://") && !strings.ContainsAny(url, "@:") && strings.Count(url, "/") == 1 {
		url = "https://github.com/" + url + ".git"
	}
	// Reject dash-leading URLs (git option injection) and non-standard
	// transports (ext::/file:: remote helpers can run commands).
	if strings.HasPrefix(url, "-") || strings.Contains(url, "::") {
		return fmt.Errorf("refusing unsafe git source %q", url)
	}
	// Disable the ext/file transports as defense-in-depth against RCE via
	// crafted URLs, then pass "--" so a URL can never be read as a flag.
	args := []string{
		"-c", "protocol.ext.allow=never", "-c", "protocol.file.allow=user",
		"clone", "--depth", "1",
	}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, "--", url, dst)
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %v: %s", url, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func isLocalPath(src string) bool {
	return strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") || strings.HasPrefix(src, "~")
}

// deriveName derives a stable name from a marketplace source.
func deriveName(src string) string {
	src = strings.TrimSuffix(src, ".git")
	if i := strings.LastIndex(src, "@"); i > 0 && !strings.Contains(src[i:], "/") {
		src = src[:i]
	}
	base := src
	if i := strings.LastIndexAny(src, "/:"); i >= 0 {
		base = src[i+1:]
	}
	if base == "" {
		base = "marketplace"
	}
	return base
}

func upsertMarketplace(list []MarketplaceRef, m MarketplaceRef) []MarketplaceRef {
	for i := range list {
		if list[i].Name == m.Name {
			list[i] = m
			return list
		}
	}
	return append(list, m)
}

func upsertPlugin(list []InstalledPlugin, p InstalledPlugin) []InstalledPlugin {
	for i := range list {
		if list[i].Name == p.Name {
			list[i] = p
			return list
		}
	}
	return append(list, p)
}

// copyDir recursively copies src to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.Mode()&os.ModeSymlink != 0 {
			return nil // never follow symlinks (they can point at host secrets)
		}
		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".git") && rel != "." {
				return filepath.SkipDir // don't copy .git
			}
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
