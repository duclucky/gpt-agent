package main

import (
	"context"
	"encoding/json"
	"fmt"
)

func (n *nativeTools) memoryRememberTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Content, Kind, Source, Scope string
		Tags                         []string
		Importance                   float64
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Kind == "" {
		a.Kind = "fact"
	}
	if a.Source == "" {
		a.Source = "manual"
	}
	if a.Scope == "" {
		a.Scope = "global"
	}
	if !rawHasJSONKey(raw, "importance") {
		a.Importance = .5
	}
	return n.learning.remember(a.Content, a.Kind, a.Tags, a.Source, a.Importance, a.Scope)
}
func (n *nativeTools) memorySearchTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Query         string
		Limit         int
		Kinds, Scopes []string
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit == 0 {
		a.Limit = 10
	}
	items, err := n.learning.search(a.Query, a.Limit, a.Kinds, a.Scopes)
	if err != nil {
		return nil, err
	}
	return map[string]any{"memories": items}, nil
}
func (n *nativeTools) memoryForgetTool(raw json.RawMessage) (map[string]any, error) {
	var a struct{ ID string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, nativeToolError{"id is required"}
	}
	return n.learning.forget(a.ID)
}
func (n *nativeTools) skillUpsertTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Name, Description, Content, CreatedBy, Version string
		Tags                                           []string
		Pinned                                         bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.CreatedBy == "" {
		a.CreatedBy = "agent"
	}
	if a.Version == "" {
		a.Version = "1.0.0"
	}
	return n.learning.upsertSkill(a.Name, a.Description, a.Content, a.Tags, a.CreatedBy, a.Pinned, a.Version)
}
func (n *nativeTools) skillListTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Query           string
		IncludeArchived bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	items, err := n.learning.listSkills(a.Query, a.IncludeArchived)
	if err != nil {
		return nil, err
	}
	return map[string]any{"skills": items}, nil
}
func (n *nativeTools) skillGetTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Slug      string
		RecordUse *bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	record := true
	if a.RecordUse != nil {
		record = *a.RecordUse
	}
	return n.learning.getSkill(a.Slug, record)
}
func (n *nativeTools) skillUsageTool(raw json.RawMessage) (map[string]any, error) {
	var a struct{ Slug, Event string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Event == "" {
		a.Event = "use"
	}
	return n.learning.recordUse(a.Slug, a.Event)
}
func (n *nativeTools) skillPinTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		Slug   string
		Pinned bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.learning.pinSkill(a.Slug, a.Pinned)
}
func (n *nativeTools) skillArchiveTool(raw json.RawMessage) (map[string]any, error) {
	var a struct{ Slug string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.learning.archiveSkill(a.Slug)
}
func (n *nativeTools) skillRestoreTool(raw json.RawMessage) (map[string]any, error) {
	var a struct{ Slug string }
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	return n.learning.restoreSkill(a.Slug)
}
func (n *nativeTools) skillCurateTool(raw json.RawMessage) (map[string]any, error) {
	var a struct {
		StaleAfterDays int
		DryRun         *bool
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.StaleAfterDays == 0 {
		a.StaleAfterDays = 60
	}
	dry := true
	if a.DryRun != nil {
		dry = *a.DryRun
	}
	return n.learning.curate(a.StaleAfterDays, dry)
}
func (n *nativeTools) learningStatsTool() (map[string]any, error) {
	stats, err := n.learning.stats()
	if err != nil {
		return nil, err
	}
	if n.evolution != nil {
		stats["evolution"] = n.evolution.stats()
	}
	return stats, nil
}
func (n *nativeTools) codingSkillsInstallTool() (map[string]any, error) {
	return n.learning.installCodingPack()
}
func (n *nativeTools) learnTool(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var a struct {
		WorkspaceID, Path string
		Memories          []struct {
			Content, Kind, Source string
			Tags                  []string
			Importance            float64
		}
		Skills []struct {
			Name, Description, Content, CreatedBy, Version string
			Tags                                           []string
			Pinned                                         bool
		}
	}
	if err := decodeNativeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Path == "" {
		a.Path = "."
	}
	scope := "global"
	var project map[string]any
	if a.WorkspaceID != "" {
		p, err := n.projectInspect(ctx, a.WorkspaceID, a.Path, false, 10000)
		if err != nil {
			return nil, err
		}
		scope = projectLearningScopeNative(p)
		project = map[string]any{"workspaceId": p["workspaceId"], "path": p["path"], "projectRoot": p["projectRoot"], "gitRoot": p["gitRoot"]}
	}
	savedMem := []map[string]any{}
	for _, m := range a.Memories {
		kind := m.Kind
		if kind == "" {
			kind = "lesson"
		}
		source := m.Source
		if source == "" {
			source = "task"
		}
		importance := m.Importance
		if importance == 0 && !rawHasJSONKey(raw, "importance") {
			importance = .6
		}
		v, err := n.learning.remember(m.Content, kind, m.Tags, source, importance, scope)
		if err != nil {
			return nil, err
		}
		savedMem = append(savedMem, v)
	}
	savedSkills := []map[string]any{}
	for _, s := range a.Skills {
		created := s.CreatedBy
		if created == "" {
			created = "agent"
		}
		version := s.Version
		if version == "" {
			version = "1.0.0"
		}
		v, err := n.learning.upsertSkill(s.Name, s.Description, s.Content, s.Tags, created, s.Pinned, version)
		if err != nil {
			return nil, err
		}
		savedSkills = append(savedSkills, v)
	}
	return map[string]any{"memories": savedMem, "skills": savedSkills, "scope": scope, "project": project}, nil
}
func projectLearningScopeNative(project map[string]any) string {
	root, _ := project["gitRoot"].(string)
	if root == "" {
		root, _ = project["projectRoot"].(string)
	}
	if root == "" {
		root = fmt.Sprintf("%v:%v", project["workspaceId"], project["path"])
	}
	return "project:" + normalizeScope(root)
}
