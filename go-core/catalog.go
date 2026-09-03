package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed tool_catalog.json
var embeddedToolCatalog []byte

type toolCatalog struct {
	Tools  []map[string]any
	ByName map[string]map[string]any
}

func loadToolCatalog() (*toolCatalog, error) {
	var tools []map[string]any
	if err := json.Unmarshal(embeddedToolCatalog, &tools); err != nil {
		return nil, fmt.Errorf("parse embedded tool catalog: %w", err)
	}
	byName := make(map[string]map[string]any, len(tools))
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			return nil, fmt.Errorf("tool catalog contains entry without name")
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("duplicate tool in catalog: %s", name)
		}
		byName[name] = tool
	}
	return &toolCatalog{Tools: tools, ByName: byName}, nil
}

func (c *toolCatalog) names() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.ByName))
	for name := range c.ByName {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (c *toolCatalog) has(name string) bool {
	if c == nil {
		return false
	}
	_, ok := c.ByName[name]
	return ok
}
