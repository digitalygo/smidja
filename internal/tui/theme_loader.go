package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const ColorModeUnset ColorMode = -1

var colorModeOverride ColorMode = ColorModeUnset

func DetectColorMode() ColorMode {
	if colorModeOverride != ColorModeUnset {
		return colorModeOverride
	}
	colorTerm := os.Getenv("COLORTERM")
	if strings.Contains(colorTerm, "truecolor") || strings.Contains(colorTerm, "24bit") {
		return ColorModeTrueColor
	}
	return ColorMode256
}

func SetColorModeOverride(mode ColorMode) { colorModeOverride = mode }

type ThemeSource struct {
	Name string
	Path string
}

type ThemeRegistry struct {
	mu         sync.Mutex
	builtin    map[string]themeJSON
	packageDir string
	userDir    string
	mode       ColorMode

	activeName   string
	active       *Theme
	activeSource string

	watchInterval time.Duration
	stopWatch     chan struct{}
	watchDone     chan struct{}
	onReload      func()
}

func NewThemeRegistry(userDir, packageDir string, mode ColorMode) *ThemeRegistry {
	registry := &ThemeRegistry{
		builtin:    map[string]themeJSON{},
		packageDir: packageDir,
		userDir:    userDir,
		mode:       mode,
	}
	dark, err := parseThemeJSON("dark", themeDarkJSON)
	if err == nil {
		registry.builtin["dark"] = dark
	}
	light, err := parseThemeJSON("light", themeLightJSON)
	if err == nil {
		registry.builtin["light"] = light
	}
	return registry
}

func UserThemesDir(home string) string {
	return filepath.Join(home, ".smidja", "themes")
}

func (r *ThemeRegistry) sources() map[string]string {
	sources := make(map[string]string)
	for name := range r.builtin {
		sources[name] = ""
	}
	for name, path := range themeFilesInDir(r.packageDir) {
		sources[name] = path
	}
	for name, path := range themeFilesInDir(r.userDir) {
		sources[name] = path
	}
	return sources
}

func themeFilesInDir(dir string) map[string]string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	found := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		document, err := loadThemeDocument(path)
		if err != nil || document.Name == "" || strings.Contains(document.Name, "/") {
			continue
		}
		found[document.Name] = path
	}
	return found
}

func loadThemeDocument(path string) (themeJSON, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return themeJSON{}, err
	}
	return parseThemeJSON(path, string(content))
}

func (r *ThemeRegistry) AvailableThemes() []ThemeSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	sources := r.sources()
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]ThemeSource, 0, len(names))
	for _, name := range names {
		result = append(result, ThemeSource{Name: name, Path: sources[name]})
	}
	return result
}

func (r *ThemeRegistry) loadByName(name string) (*Theme, error) {
	sources := r.sources()
	path, ok := sources[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	var document themeJSON
	var err error
	if path == "" {
		document, ok = r.builtin[name]
		if !ok {
			return nil, os.ErrNotExist
		}
	} else {
		document, err = loadThemeDocument(path)
		if err != nil {
			return nil, err
		}
	}
	return newTheme(document, r.mode, path)
}

func (r *ThemeRegistry) SetTheme(name string) (*Theme, error) {
	r.mu.Lock()
	theme, err := r.loadByName(name)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.activeName = name
	r.active = theme
	r.activeSource = theme.SourcePath
	interval := r.watchInterval
	r.mu.Unlock()

	r.restartWatcher(interval)
	return theme, nil
}

func (r *ThemeRegistry) Active() *Theme {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *ThemeRegistry) ActiveName() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.activeName
}

func (r *ThemeRegistry) ReloadActive() (*Theme, bool) {
	r.mu.Lock()
	if r.activeName == "" || r.activeSource == "" {
		r.mu.Unlock()
		return nil, false
	}
	name := r.activeName
	theme, err := r.loadByName(name)
	if err != nil {
		r.mu.Unlock()
		return nil, false
	}
	r.active = theme
	r.activeSource = theme.SourcePath
	callback := r.onReload
	r.mu.Unlock()
	if callback != nil {
		callback()
	}
	return theme, true
}

func (r *ThemeRegistry) OnReload(callback func()) {
	r.mu.Lock()
	r.onReload = callback
	r.mu.Unlock()
}

func (r *ThemeRegistry) StartWatching(interval time.Duration) {
	r.mu.Lock()
	r.watchInterval = interval
	r.mu.Unlock()
	r.restartWatcher(interval)
}

func (r *ThemeRegistry) restartWatcher(interval time.Duration) {
	r.StopWatching()

	r.mu.Lock()
	r.watchInterval = interval
	source := r.activeSource
	name := r.activeName
	callback := r.onReload
	r.mu.Unlock()

	if source == "" || interval <= 0 {
		return
	}
	var lastMod time.Time
	if info, err := os.Stat(source); err == nil {
		lastMod = info.ModTime()
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	r.mu.Lock()
	r.stopWatch = stop
	r.watchDone = done
	r.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				info, err := os.Stat(source)
				if err != nil || info.ModTime().Equal(lastMod) {
					continue
				}
				theme, loadErr := r.loadByNamePublic(name)
				if loadErr != nil {
					continue
				}
				lastMod = info.ModTime()
				r.mu.Lock()
				r.active = theme
				r.activeSource = theme.SourcePath
				r.mu.Unlock()
				if callback != nil {
					callback()
				}
			}
		}
	}()
}

func (r *ThemeRegistry) loadByNamePublic(name string) (*Theme, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadByName(name)
}

func (r *ThemeRegistry) StopWatching() {
	r.mu.Lock()
	stop := r.stopWatch
	done := r.watchDone
	r.stopWatch = nil
	r.watchDone = nil
	r.mu.Unlock()
	if stop != nil {
		close(stop)
	}
	if done != nil {
		<-done
	}
}
