package agents

import (
	"fmt"
	"sort"

	"github.com/digitalygo/smidja/internal/content"
)

type Info struct {
	Name        string
	DisplayName string
	Description string
	Package     string
	Path        string
	Tier        content.Tier
	Origin      string
}

type Entry struct {
	Definition Definition
	Info       Info
}

type Catalog struct {
	definitions map[string]Definition
	infos       []Info
	byName      map[string]Info
	parseErrors map[string]error
}

func NewCatalog(snapshot content.Snapshot) Catalog {
	catalog := Catalog{
		definitions: make(map[string]Definition, len(snapshot.Agents)),
		byName:      make(map[string]Info, len(snapshot.Agents)),
		parseErrors: make(map[string]error),
	}
	for name, ref := range snapshot.Agents {
		if !safeAgentName(name) {
			continue
		}
		definition, err := parseDefinition(ref)
		if err != nil {
			catalog.parseErrors[name] = fmt.Errorf("agent %q: %w", bounded(name), err)
			continue
		}
		definition.Name = name
		definition.DisplayName = sanitizeDisplayText(definition.DisplayName)
		if definition.DisplayName == "" {
			definition.DisplayName = name
		}
		catalog.definitions[name] = definition
	}
	names := catalog.Names()
	catalog.infos = make([]Info, 0, len(names))
	for _, name := range names {
		definition := catalog.definitions[name]
		info := Info{
			Name:        name,
			DisplayName: definition.DisplayName,
			Description: definition.Description,
			Package:     definition.Package,
			Path:        definition.Path,
			Tier:        definition.Tier,
			Origin:      definition.Origin,
		}
		catalog.infos = append(catalog.infos, info)
		catalog.byName[name] = info
	}
	return catalog
}

func (c Catalog) Names() []string {
	names := make([]string, 0, len(c.definitions))
	for name := range c.definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c Catalog) Infos() []Info {
	infos := make([]Info, len(c.infos))
	copy(infos, c.infos)
	return infos
}

func (c Catalog) Entries() []Entry {
	entries := make([]Entry, 0, len(c.infos))
	for _, info := range c.infos {
		entries = append(entries, Entry{Definition: c.definitions[info.Name], Info: info})
	}
	return entries
}

func (c Catalog) Lookup(name string) (Definition, bool) {
	definition, ok := c.definitions[name]
	return definition, ok
}

func (c Catalog) Info(name string) (Info, bool) {
	info, ok := c.byName[name]
	return info, ok
}

func (c Catalog) ParseError(name string) error {
	err, ok := c.parseErrors[name]
	if !ok {
		return nil
	}
	return err
}

func (c Catalog) Len() int {
	return len(c.definitions)
}
