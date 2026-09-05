package main

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

//go:embed coding_skill_pack.json
var embeddedCodingSkillPack []byte

const codingSkillPackVersion = "1.4.0"

type memoryEntry struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags"`
	Source      string   `json:"source"`
	Scope       string   `json:"scope"`
	Importance  float64  `json:"importance"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	Fingerprint string   `json:"fingerprint"`
}
type memoryDB struct {
	Version int           `json:"version"`
	Entries []memoryEntry `json:"entries"`
}
type skillMetadata struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	CreatedBy   string   `json:"created_by"`
	Tags        []string `json:"tags"`
	Pinned      bool     `json:"pinned"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
}
type skillUsageRow struct {
	UseCount       int    `json:"use_count"`
	ViewCount      int    `json:"view_count"`
	PatchCount     int    `json:"patch_count"`
	LastActivityAt any    `json:"last_activity_at"`
	State          string `json:"state"`
	Pinned         bool   `json:"pinned"`
}
type skillUsageDB struct {
	Version int                      `json:"version"`
	Skills  map[string]skillUsageRow `json:"skills"`
}
type listedSkill struct {
	Slug     string
	Archived bool
	Score    float64
	Metadata skillMetadata
	Content  string
}
type embeddedSkill struct {
	Slug     string        `json:"slug"`
	Metadata skillMetadata `json:"metadata"`
	Content  string        `json:"content"`
}

type learningStore struct {
	root, memoryPath, skillsDir, archiveDir, usagePath string
	memoryMu                                           sync.Mutex
	usageMu                                            sync.Mutex
}

func newLearningStore(dataRoot string) *learningStore {
	root := filepath.Join(dataRoot, "learning")
	skills := filepath.Join(root, "skills")
	return &learningStore{root: root, memoryPath: filepath.Join(root, "memory.json"), skillsDir: skills, archiveDir: filepath.Join(skills, ".archive"), usagePath: filepath.Join(root, "skill-usage.json")}
}
func nowISO() string                { return time.Now().UTC().Format(time.RFC3339Nano) }
func normalizeText(s string) string { return strings.Join(strings.Fields(strings.TrimSpace(s)), " ") }
func normalizeScope(s string) string {
	v := strings.ToLower(filepath.ToSlash(normalizeText(s)))
	if v == "" {
		return "global"
	}
	return v
}

var viReplacer = strings.NewReplacer(
	"à", "a", "á", "a", "ạ", "a", "ả", "a", "ã", "a", "â", "a", "ầ", "a", "ấ", "a", "ậ", "a", "ẩ", "a", "ẫ", "a", "ă", "a", "ằ", "a", "ắ", "a", "ặ", "a", "ẳ", "a", "ẵ", "a",
	"è", "e", "é", "e", "ẹ", "e", "ẻ", "e", "ẽ", "e", "ê", "e", "ề", "e", "ế", "e", "ệ", "e", "ể", "e", "ễ", "e",
	"ì", "i", "í", "i", "ị", "i", "ỉ", "i", "ĩ", "i", "ò", "o", "ó", "o", "ọ", "o", "ỏ", "o", "õ", "o", "ô", "o", "ồ", "o", "ố", "o", "ộ", "o", "ổ", "o", "ỗ", "o", "ơ", "o", "ờ", "o", "ớ", "o", "ợ", "o", "ở", "o", "ỡ", "o",
	"ù", "u", "ú", "u", "ụ", "u", "ủ", "u", "ũ", "u", "ư", "u", "ừ", "u", "ứ", "u", "ự", "u", "ử", "u", "ữ", "u", "ỳ", "y", "ý", "y", "ỵ", "y", "ỷ", "y", "ỹ", "y", "đ", "d",
	"À", "A", "Á", "A", "Ạ", "A", "Ả", "A", "Ã", "A", "Â", "A", "Ầ", "A", "Ấ", "A", "Ậ", "A", "Ẩ", "A", "Ẫ", "A", "Ă", "A", "Ằ", "A", "Ắ", "A", "Ặ", "A", "Ẳ", "A", "Ẵ", "A",
	"È", "E", "É", "E", "Ẹ", "E", "Ẻ", "E", "Ẽ", "E", "Ê", "E", "Ề", "E", "Ế", "E", "Ệ", "E", "Ể", "E", "Ễ", "E", "Ì", "I", "Í", "I", "Ị", "I", "Ỉ", "I", "Ĩ", "I",
	"Ò", "O", "Ó", "O", "Ọ", "O", "Ỏ", "O", "Õ", "O", "Ô", "O", "Ồ", "O", "Ố", "O", "Ộ", "O", "Ổ", "O", "Ỗ", "O", "Ơ", "O", "Ờ", "O", "Ớ", "O", "Ợ", "O", "Ở", "O", "Ỡ", "O",
	"Ù", "U", "Ú", "U", "Ụ", "U", "Ủ", "U", "Ũ", "U", "Ư", "U", "Ừ", "U", "Ứ", "U", "Ự", "U", "Ử", "U", "Ữ", "U", "Ỳ", "Y", "Ý", "Y", "Ỵ", "Y", "Ỷ", "Y", "Ỹ", "Y", "Đ", "D")
var tokenStopwords = map[string]bool{"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true, "code": true, "coding": true, "for": true, "from": true, "in": true, "is": true, "it": true, "of": true, "on": true, "or": true, "gpt": true, "agent": true, "task": true, "tasks": true, "the": true, "this": true, "to": true, "use": true, "using": true, "with": true, "workflow": true, "cac": true, "cho": true, "co": true, "cua": true, "da": true, "dang": true, "de": true, "duoc": true, "khi": true, "khong": true, "la": true, "lam": true, "mot": true, "nay": true, "nhung": true, "sau": true, "thi": true, "trong": true, "truoc": true, "va": true, "viec": true, "voi": true}

func normalizeSearch(s string) string { return strings.ToLower(viReplacer.Replace(normalizeText(s))) }
func tokenSet(s string, drop bool) map[string]bool {
	parts := strings.FieldsFunc(normalizeSearch(s), func(r rune) bool { return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') })
	out := map[string]bool{}
	for _, p := range parts {
		if len([]rune(p)) <= 1 || (drop && tokenStopwords[p]) {
			continue
		}
		out[p] = true
	}
	return out
}
func scoreText(query, text string) float64 {
	q, t := tokenSet(query, true), tokenSet(text, true)
	if len(q) == 0 {
		return 0
	}
	hits := 0
	for x := range q {
		if t[x] {
			hits++
		}
	}
	return float64(hits) / float64(len(q))
}
func slugify(s string) (string, error) {
	s = normalizeSearch(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if b.Len() > 0 && !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "-")
	}
	if slug == "" {
		return "", errors.New("Skill name must contain at least one alphanumeric character.")
	}
	return slug, nil
}
func shaHex(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func memoryFingerprint(scope, kind, text string) string {
	return shaHex(scope + "\n" + kind + "\n" + strings.ToLower(text))
}
func legacyMemoryFingerprint(kind, text string) string {
	return shaHex(kind + "\n" + strings.ToLower(text))
}
func uuidV4() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func readJSONFile(path string, target any, defaultJSON string) error {
	buf, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		buf = []byte(defaultJSON)
	} else if err != nil {
		return err
	}
	return json.Unmarshal(buf, target)
}
func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	tmp := fmt.Sprintf("%s.%d.%s.tmp", path, os.Getpid(), randomHex(4))
	if err := os.WriteFile(tmp, buf, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
func uniqueNormalized(items []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range items {
		v := normalizeText(x)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *learningStore) remember(content, kind string, tags []string, source string, importance float64, scope string) (map[string]any, error) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	text := normalizeText(content)
	if text == "" {
		return nil, errors.New("Memory content is required.")
	}
	if kind = normalizeText(kind); kind == "" {
		kind = "fact"
	}
	scope = normalizeScope(scope)
	if source = normalizeText(source); source == "" {
		source = "manual"
	}
	if importance < 0 {
		importance = 0
	}
	if importance > 1 {
		importance = 1
	}
	var db memoryDB
	if err := readJSONFile(s.memoryPath, &db, `{"version":2,"entries":[]}`); err != nil {
		return nil, err
	}
	if db.Version < 2 {
		db.Version = 2
	}
	fp := memoryFingerprint(scope, kind, text)
	legacy := ""
	if scope == "global" {
		legacy = legacyMemoryFingerprint(kind, text)
	}
	for i := range db.Entries {
		e := &db.Entries[i]
		if e.Fingerprint == fp || (legacy != "" && e.Fingerprint == legacy && normalizeScope(e.Scope) == "global") {
			e.UpdatedAt = nowISO()
			e.Scope = scope
			e.Fingerprint = fp
			e.Tags = uniqueNormalized(append(e.Tags, tags...))
			if importance > e.Importance {
				e.Importance = importance
			}
			if err := writeJSONAtomic(s.memoryPath, &db); err != nil {
				return nil, err
			}
			return memoryMap(*e, true), nil
		}
	}
	now := nowISO()
	e := memoryEntry{ID: uuidV4(), Kind: kind, Content: text, Tags: uniqueNormalized(tags), Source: source, Scope: scope, Importance: importance, CreatedAt: now, UpdatedAt: now, Fingerprint: fp}
	db.Entries = append(db.Entries, e)
	if err := writeJSONAtomic(s.memoryPath, &db); err != nil {
		return nil, err
	}
	return memoryMap(e, false), nil
}
func memoryMap(e memoryEntry, dedup bool) map[string]any {
	return map[string]any{"id": e.ID, "kind": e.Kind, "content": e.Content, "tags": e.Tags, "source": e.Source, "scope": e.Scope, "importance": e.Importance, "createdAt": e.CreatedAt, "updatedAt": e.UpdatedAt, "fingerprint": e.Fingerprint, "deduplicated": dedup}
}
func (s *learningStore) search(query string, limit int, kinds, scopes []string) ([]map[string]any, error) {
	var db memoryDB
	if err := readJSONFile(s.memoryPath, &db, `{"version":2,"entries":[]}`); err != nil {
		return nil, err
	}
	kindSet := map[string]bool{}
	for _, k := range kinds {
		if v := normalizeText(k); v != "" {
			kindSet[v] = true
		}
	}
	scopeSet := map[string]bool{}
	for _, sc := range scopes {
		scopeSet[normalizeScope(sc)] = true
	}
	now := time.Now()
	out := []map[string]any{}
	for _, e := range db.Entries {
		sc := normalizeScope(e.Scope)
		if len(kindSet) > 0 && !kindSet[e.Kind] {
			continue
		}
		if len(scopeSet) > 0 && !scopeSet[sc] {
			continue
		}
		relevance := 1.0
		if query != "" {
			relevance = scoreText(query, e.Content+" "+strings.Join(e.Tags, " ")+" "+e.Kind)
		}
		parsed, _ := time.Parse(time.RFC3339Nano, firstNonEmpty(e.UpdatedAt, e.CreatedAt))
		age := math.Max(0, now.Sub(parsed).Hours()/24)
		recency := 1 / (1 + age/30)
		score := e.Importance*0.6 + recency*0.4
		if query != "" {
			score = relevance*0.75 + e.Importance*0.15 + recency*0.10
		}
		score = math.Round(score*10000) / 10000
		if query != "" && score <= 0.10 {
			continue
		}
		m := memoryMap(e, false)
		delete(m, "deduplicated")
		m["scope"] = sc
		m["score"] = score
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["score"].(float64) > out[j]["score"].(float64) })
	if limit < 1 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *learningStore) forget(id string) (map[string]any, error) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	var db memoryDB
	if err := readJSONFile(s.memoryPath, &db, `{"version":2,"entries":[]}`); err != nil {
		return nil, err
	}
	out := db.Entries[:0]
	found := false
	for _, e := range db.Entries {
		if e.ID == id {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return map[string]any{"forgotten": false, "id": id}, nil
	}
	db.Entries = out
	if err := writeJSONAtomic(s.memoryPath, &db); err != nil {
		return nil, err
	}
	return map[string]any{"forgotten": true, "id": id}, nil
}

func skillMarkdown(meta skillMetadata, content string) string {
	tags := make([]string, len(meta.Tags))
	for i, t := range meta.Tags {
		tags[i] = `"` + strings.ReplaceAll(t, `"`, `\"`) + `"`
	}
	return fmt.Sprintf("---\nname: %s\ndescription: %s\nversion: %s\ncreated_by: %s\ntags: [%s]\n---\n\n%s\n", meta.Name, meta.Description, meta.Version, meta.CreatedBy, strings.Join(tags, ", "), strings.TrimSpace(content))
}
func (s *learningStore) upsertSkill(name, description, content string, tags []string, createdBy string, pinned bool, version string) (map[string]any, error) {
	slug, err := slugify(name)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.skillsDir, slug)
	metaPath := filepath.Join(dir, "metadata.json")
	var prior skillMetadata
	priorExists := readJSONFile(metaPath, &prior, `{}`) == nil && prior.Name != ""
	if createdBy != "user" {
		createdBy = "agent"
	}
	if version == "" {
		version = "1.0.0"
	}
	created := prior.CreatedAt
	if created == "" {
		created = nowISO()
	}
	meta := skillMetadata{Name: normalizeText(name), Description: normalizeText(description), Version: normalizeText(version), CreatedBy: createdBy, Tags: uniqueNormalized(tags), Pinned: pinned, CreatedAt: created, UpdatedAt: nowISO()}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	md := skillMarkdown(meta, content)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0644); err != nil {
		return nil, err
	}
	if err := writeJSONAtomic(metaPath, &meta); err != nil {
		return nil, err
	}
	return map[string]any{"slug": slug, "path": filepath.Join(dir, "SKILL.md"), "metadata": meta, "updated": priorExists}, nil
}
func (s *learningStore) getSkill(slug string, record bool) (map[string]any, error) {
	safe, err := slugify(slug)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(s.skillsDir, safe)
	var meta skillMetadata
	if err := readJSONFile(filepath.Join(dir, "metadata.json"), &meta, `{}`); err != nil || meta.Name == "" {
		return nil, fmt.Errorf("Skill not found: %s", safe)
	}
	buf, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	if record {
		if _, err := s.recordUse(safe, "use"); err != nil {
			return nil, err
		}
	}
	return map[string]any{"slug": safe, "metadata": meta, "content": string(buf)}, nil
}
func (s *learningStore) scanSkills(base string, archived bool) ([]listedSkill, error) {
	if err := os.MkdirAll(base, 0755); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	out := []listedSkill{}
	for _, e := range ents {
		if !e.IsDir() || e.Name() == ".archive" {
			continue
		}
		dir := filepath.Join(base, e.Name())
		var meta skillMetadata
		if readJSONFile(filepath.Join(dir, "metadata.json"), &meta, `{}`) != nil || meta.Name == "" {
			continue
		}
		buf, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		out = append(out, listedSkill{Slug: e.Name(), Archived: archived, Score: 1, Metadata: meta, Content: string(buf)})
	}
	return out, nil
}
func (s *learningStore) listSkills(query string, includeArchived bool) ([]map[string]any, error) {
	active, err := s.scanSkills(s.skillsDir, false)
	if err != nil {
		return nil, err
	}
	all := active
	if includeArchived {
		arch, _ := s.scanSkills(s.archiveDir, true)
		all = append(all, arch...)
	}
	if query != "" {
		all = rankSkills(query, all)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Metadata.Name < all[j].Metadata.Name
	})
	out := []map[string]any{}
	for _, x := range all {
		if query != "" && x.Score <= 0 {
			continue
		}
		out = append(out, map[string]any{"slug": x.Slug, "archived": x.Archived, "score": x.Score, "metadata": x.Metadata})
	}
	return out, nil
}
func rankSkills(query string, items []listedSkill) []listedSkill {
	q := tokenSet(query, true)
	if len(q) == 0 {
		return items
	}
	type prep struct {
		item                           listedSkill
		name, desc, tags, content, all map[string]bool
	}
	ps := make([]prep, 0, len(items))
	for _, it := range items {
		name := tokenSet(it.Metadata.Name, true)
		desc := tokenSet(it.Metadata.Description, true)
		tags := tokenSet(strings.Join(it.Metadata.Tags, " "), true)
		content := tokenSet(it.Content, true)
		all := map[string]bool{}
		for _, m := range []map[string]bool{name, desc, tags, content} {
			for k := range m {
				all[k] = true
			}
		}
		ps = append(ps, prep{it, name, desc, tags, content, all})
	}
	df := map[string]int{}
	for t := range q {
		for _, p := range ps {
			if p.all[t] {
				df[t]++
			}
		}
	}
	idf := func(t string) float64 { return math.Log(float64(len(ps)+1)/float64(df[t]+1)) + 1 }
	den := 0.0
	for t := range q {
		den += idf(t) * 3
	}
	if den == 0 {
		den = 1
	}
	out := make([]listedSkill, 0, len(ps))
	for _, p := range ps {
		raw := 0.0
		for t := range q {
			boost := 0.0
			if p.name[t] || p.tags[t] {
				boost = 3
			} else if p.desc[t] {
				boost = 1.75
			} else if p.content[t] {
				boost = 1
			}
			if boost > 0 {
				raw += idf(t) * boost
			}
		}
		p.item.Score = math.Round(math.Min(1, raw/den)*10000) / 10000
		out = append(out, p.item)
	}
	return out
}
func (s *learningStore) recordUse(slug, event string) (map[string]any, error) {
	safe, err := slugify(slug)
	if err != nil {
		return nil, err
	}
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	var db skillUsageDB
	if err := readJSONFile(s.usagePath, &db, `{"version":1,"skills":{}}`); err != nil {
		return nil, err
	}
	if db.Skills == nil {
		db.Skills = map[string]skillUsageRow{}
	}
	row := db.Skills[safe]
	if row.State == "" {
		row.State = "active"
	}
	switch event {
	case "view":
		row.ViewCount++
	case "patch":
		row.PatchCount++
	default:
		row.UseCount++
	}
	var meta skillMetadata
	if readJSONFile(filepath.Join(s.skillsDir, safe, "metadata.json"), &meta, `{}`) == nil && meta.Name != "" {
		row.Pinned = meta.Pinned
	}
	row.LastActivityAt = nowISO()
	db.Skills[safe] = row
	if err := writeJSONAtomic(s.usagePath, &db); err != nil {
		return nil, err
	}
	return map[string]any{"slug": safe, "use_count": row.UseCount, "view_count": row.ViewCount, "patch_count": row.PatchCount, "last_activity_at": row.LastActivityAt, "state": row.State, "pinned": row.Pinned}, nil
}
func (s *learningStore) pinSkill(slug string, pinned bool) (map[string]any, error) {
	safe, err := slugify(slug)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(s.skillsDir, safe, "metadata.json")
	var meta skillMetadata
	if readJSONFile(path, &meta, `{}`) != nil || meta.Name == "" {
		return nil, fmt.Errorf("Skill not found: %s", safe)
	}
	meta.Pinned = pinned
	meta.UpdatedAt = nowISO()
	if err := writeJSONAtomic(path, &meta); err != nil {
		return nil, err
	}
	s.usageMu.Lock()
	var db skillUsageDB
	_ = readJSONFile(s.usagePath, &db, `{"version":1,"skills":{}}`)
	if db.Skills == nil {
		db.Skills = map[string]skillUsageRow{}
	}
	row := db.Skills[safe]
	if row.State == "" {
		row.State = "active"
	}
	row.Pinned = pinned
	db.Skills[safe] = row
	err = writeJSONAtomic(s.usagePath, &db)
	s.usageMu.Unlock()
	if err != nil {
		return nil, err
	}
	return map[string]any{"slug": safe, "pinned": pinned}, nil
}
func (s *learningStore) archiveSkill(slug string) (map[string]any, error) {
	safe, err := slugify(slug)
	if err != nil {
		return nil, err
	}
	src := filepath.Join(s.skillsDir, safe)
	var meta skillMetadata
	if readJSONFile(filepath.Join(src, "metadata.json"), &meta, `{}`) != nil || meta.Name == "" {
		return nil, fmt.Errorf("Skill not found: %s", safe)
	}
	if meta.Pinned {
		return nil, errors.New("Pinned skills cannot be archived.")
	}
	if err := os.MkdirAll(s.archiveDir, 0755); err != nil {
		return nil, err
	}
	dest := filepath.Join(s.archiveDir, safe)
	if _, err := os.Stat(dest); err == nil {
		dest = filepath.Join(s.archiveDir, fmt.Sprintf("%s-%d", safe, time.Now().UnixMilli()))
	}
	if err := os.Rename(src, dest); err != nil {
		return nil, err
	}
	s.setUsageState(safe, "archived")
	return map[string]any{"archived": true, "slug": safe, "path": dest}, nil
}
func (s *learningStore) restoreSkill(slug string) (map[string]any, error) {
	safe, err := slugify(slug)
	if err != nil {
		return nil, err
	}
	source := filepath.Join(s.archiveDir, safe)
	if _, err := os.Stat(source); err != nil {
		ents, _ := os.ReadDir(s.archiveDir)
		matches := []string{}
		for _, e := range ents {
			if e.Name() == safe || strings.HasPrefix(e.Name(), safe+"-") {
				matches = append(matches, e.Name())
			}
		}
		sort.Strings(matches)
		if len(matches) == 0 {
			return nil, fmt.Errorf("Archived skill not found: %s", safe)
		}
		source = filepath.Join(s.archiveDir, matches[len(matches)-1])
	}
	dest := filepath.Join(s.skillsDir, safe)
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("Active skill already exists: %s", safe)
	}
	if err := os.Rename(source, dest); err != nil {
		return nil, err
	}
	s.setUsageState(safe, "active")
	return map[string]any{"restored": true, "slug": safe, "path": dest}, nil
}
func (s *learningStore) setUsageState(slug, state string) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	var db skillUsageDB
	_ = readJSONFile(s.usagePath, &db, `{"version":1,"skills":{}}`)
	if db.Skills == nil {
		db.Skills = map[string]skillUsageRow{}
	}
	row := db.Skills[slug]
	row.State = state
	row.LastActivityAt = nowISO()
	db.Skills[slug] = row
	_ = writeJSONAtomic(s.usagePath, &db)
}
func (s *learningStore) curate(days int, dry bool) (map[string]any, error) {
	items, err := s.listSkills("", false)
	if err != nil {
		return nil, err
	}
	var usage skillUsageDB
	_ = readJSONFile(s.usagePath, &usage, `{"version":1,"skills":{}}`)
	candidates := []map[string]any{}
	now := time.Now()
	for _, it := range items {
		slug := it["slug"].(string)
		meta := it["metadata"].(skillMetadata)
		u := usage.Skills[slug]
		if meta.Pinned || u.Pinned {
			continue
		}
		lastText := meta.UpdatedAt
		if s, ok := u.LastActivityAt.(string); ok && s != "" {
			lastText = s
		}
		last, _ := time.Parse(time.RFC3339Nano, lastText)
		age := int(now.Sub(last).Hours() / 24)
		if age >= days {
			candidates = append(candidates, map[string]any{"slug": slug, "name": meta.Name, "ageDays": age, "useCount": u.UseCount})
		}
	}
	archived := []map[string]any{}
	if !dry {
		for _, c := range candidates {
			v, e := s.archiveSkill(c["slug"].(string))
			if e != nil {
				return nil, e
			}
			archived = append(archived, v)
		}
	}
	return map[string]any{"dryRun": dry, "staleAfterDays": days, "candidates": candidates, "archived": archived}, nil
}
func (s *learningStore) stats() (map[string]any, error) {
	var db memoryDB
	if err := readJSONFile(s.memoryPath, &db, `{"version":2,"entries":[]}`); err != nil {
		return nil, err
	}
	skills, err := s.listSkills("", true)
	if err != nil {
		return nil, err
	}
	var usage skillUsageDB
	_ = readJSONFile(s.usagePath, &usage, `{"version":1,"skills":{}}`)
	scopes := map[string]int{}
	for _, e := range db.Entries {
		scopes[normalizeScope(e.Scope)]++
	}
	active, arch := 0, 0
	for _, x := range skills {
		if x["archived"].(bool) {
			arch++
		} else {
			active++
		}
	}
	return map[string]any{"memoryCount": len(db.Entries), "memoryScopes": scopes, "activeSkills": active, "archivedSkills": arch, "trackedSkillUsage": len(usage.Skills), "learningDir": s.root}, nil
}
func (s *learningStore) installCodingPack() (map[string]any, error) {
	var pack []embeddedSkill
	if err := json.Unmarshal(embeddedCodingSkillPack, &pack); err != nil {
		return nil, err
	}
	installed := []map[string]any{}
	for _, item := range pack {
		dir := filepath.Join(s.skillsDir, item.Slug)
		prior := false
		if _, err := os.Stat(filepath.Join(dir, "metadata.json")); err == nil {
			prior = true
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, err
		}
		meta := item.Metadata
		if strings.TrimSpace(meta.Version) == "" {
			meta.Version = "1.0.0"
		}
		meta.Pinned = true
		meta.UpdatedAt = nowISO()
		if meta.CreatedAt == "" {
			meta.CreatedAt = nowISO()
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(item.Content), 0644); err != nil {
			return nil, err
		}
		if err := writeJSONAtomic(filepath.Join(dir, "metadata.json"), &meta); err != nil {
			return nil, err
		}
		installed = append(installed, map[string]any{"slug": item.Slug, "name": meta.Name, "pinned": true, "updated": prior})
	}
	return map[string]any{"version": codingSkillPackVersion, "count": len(installed), "skills": installed}, nil
}
