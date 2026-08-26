package skills

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const MaxSkillBytes = 100 * 1024

type Item struct {
	Package string

	Name string

	Path string

	Content string
}

type Catalog struct {
	mu    sync.RWMutex
	items map[string]Item
}

func New() *Catalog {
	return &Catalog{items: make(map[string]Item)}
}

func (c *Catalog) Add(pkg, name, content string) error {
	if pkg == "" {
		return errors.New("skills: empty package")
	}
	if name == "" {
		return errors.New("skills: empty skill name")
	}
	if err := validName(name); err != nil {
		return err
	}
	if !utf8.ValidString(content) {
		return fmt.Errorf("skills: %s/%s: content is not valid utf-8", pkg, name)
	}
	if len(content) > MaxSkillBytes {
		return fmt.Errorf("skills: %s/%s: %d bytes exceeds the %d byte cap", pkg, name, len(content), MaxSkillBytes)
	}
	key := keyFor(pkg, name)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = Item{Package: pkg, Name: name, Path: name, Content: content}
	return nil
}

func (c *Catalog) Merge(other *Catalog) error {
	if other == nil {
		return nil
	}
	for _, key := range other.Names() {
		item, ok := other.Get(key)
		if !ok {
			continue
		}
		if err := c.Add(item.Package, item.Name, item.Content); err != nil {
			return err
		}
	}
	return nil
}

func (c *Catalog) Names() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.items))
	for k := range c.items {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (c *Catalog) Get(key string) (Item, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	it, ok := c.items[key]
	return it, ok
}

func (c *Catalog) Lookup(name string) (Item, bool) {
	if it, ok := c.Get(name); ok {
		return it, true
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	var found Item
	matches := 0
	for k, it := range c.items {
		if strings.HasSuffix(k, "/"+name) {
			found = it
			matches++
		}
	}
	if matches != 1 {
		return Item{}, false
	}
	return found, true
}

func keyFor(pkg, name string) string {
	return pkg + "/" + name
}

func validName(name string) error {
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("skills: invalid skill name %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return fmt.Errorf("skills: invalid skill name %q", name)
		}
	}
	return nil
}
