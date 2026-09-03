package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func (n *nativeTools) isSecretSearchPath(ws workspaceConfig, abs string) bool {
	if n.cfg.Security.AllowSecretFiles {
		return false
	}
	rel, err := filepath.Rel(ws.Root, abs)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	base := filepath.Base(abs)
	for _, pattern := range n.cfg.Security.SecretFilePatterns {
		matchedBase, _ := wildcardMatch(pattern, base)
		matchedRel, _ := wildcardMatch(pattern, rel)
		if matchedBase || matchedRel {
			return true
		}
	}
	return false
}

func fallbackSearchRegexp(query string, regexMode, caseSensitive bool) (*regexp.Regexp, error) {
	pattern := query
	if !regexMode {
		pattern = regexp.QuoteMeta(query)
	}
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

func searchGlobMatches(glob, rel, base string) bool {
	if strings.TrimSpace(glob) == "" {
		return true
	}
	matchedRel, _ := wildcardMatch(glob, filepath.ToSlash(rel))
	matchedBase, _ := wildcardMatch(glob, base)
	return matchedRel || matchedBase
}

func (n *nativeTools) searchTextGoFallback(ctx context.Context, workspaceID, relativePath, query, glob string, maxResults int, regexMode, caseSensitive bool) (map[string]any, error) {
	ws, _, searchRoot, err := n.resolveWorkspacePath(workspaceID, relativePath)
	if err != nil {
		return nil, err
	}
	rx, err := fallbackSearchRegexp(query, regexMode, caseSensitive)
	if err != nil {
		return nil, nativeToolError{"invalid regex: " + err.Error()}
	}
	matches := make([]map[string]any, 0, min(maxResults, 64))
	errStop := errors.New("search result limit reached")

	visitFile := func(abs string, info os.FileInfo) error {
		if len(matches) >= maxResults {
			return errStop
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if info.Size() > 8<<20 || n.isSecretSearchPath(ws, abs) {
			return nil
		}
		relWorkspace, err := filepath.Rel(ws.Root, abs)
		if err != nil {
			return nil
		}
		relSearch, err := filepath.Rel(searchRoot, abs)
		if err != nil {
			relSearch = relWorkspace
		}
		if !searchGlobMatches(glob, filepath.ToSlash(relSearch), filepath.Base(abs)) {
			return nil
		}
		buf, err := os.ReadFile(abs)
		if err != nil || bytes.IndexByte(buf, 0) >= 0 {
			return nil
		}
		text := strings.ReplaceAll(string(bytes.ToValidUTF8(buf, []byte("\uFFFD"))), "\r\n", "\n")
		for i, line := range strings.Split(text, "\n") {
			indexes := rx.FindAllStringIndex(line, -1)
			if len(indexes) == 0 {
				continue
			}
			subs := make([]map[string]any, 0, len(indexes))
			for _, pair := range indexes {
				start, end := pair[0], pair[1]
				subs = append(subs, map[string]any{"start": start, "end": end, "match": line[start:end]})
			}
			matches = append(matches, map[string]any{
				"path": filepath.ToSlash(relWorkspace), "line": i + 1, "text": line, "submatches": subs,
			})
			if len(matches) >= maxResults {
				return errStop
			}
		}
		return nil
	}

	info, err := os.Stat(searchRoot)
	if err != nil {
		return nil, err
	}
	if info.Mode().IsRegular() {
		err = visitFile(searchRoot, info)
	} else if info.IsDir() {
		err = filepath.Walk(searchRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if info.IsDir() {
				if path != searchRoot {
					if _, ignored := ignoredTreeDirs[info.Name()]; ignored {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			return visitFile(path, info)
		})
	}
	if err != nil && !errors.Is(err, errStop) {
		return nil, err
	}
	return map[string]any{"engine": "go-fallback", "matches": matches, "stderr": "", "workspaceId": workspaceID, "path": relativePath}, nil
}
