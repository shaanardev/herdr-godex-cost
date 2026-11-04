package codex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var (
	ErrNilSessionIndex = errors.New("Codex session index is nil")
	ErrEmptyCodexHome  = errors.New("Codex home is empty")
)

// Maps Codex thread IDs to rollout files and tracks child threads.
type SessionIndex struct {
	// Diagnostics records damaged metadata skipped while building the index.
	Diagnostics  []error
	home         string
	files        map[string]string
	ambiguous    map[string]struct{}
	metadata     map[string]SessionMetadata
	titles       map[string]string
	children     sessionGraph
	databaseSeen bool
	edgesIndexed bool
}

func (index *SessionIndex) addDiagnostic(err error) {
	index.Diagnostics = append(index.Diagnostics, err)
}

// NewSessionIndex combines caller-owned read-only state databases. Without a
// usable database, it falls back to rollout metadata. Callers close the databases.
func NewSessionIndex(ctx context.Context, codexHome string, databases ...*sql.DB) (*SessionIndex, error) {
	if codexHome == "" {
		return nil, ErrEmptyCodexHome
	}

	index := &SessionIndex{
		home:      codexHome,
		files:     make(map[string]string),
		ambiguous: make(map[string]struct{}),
		metadata:  make(map[string]SessionMetadata),
		titles:    make(map[string]string),
		children:  sessionGraph{},
	}
	if err := index.populate(ctx, databases); err != nil {
		return nil, err
	}
	return index, nil
}

// Returns the indexed rollout path for a thread, if present.
func (index *SessionIndex) RolloutPath(threadID string) (string, error) {
	if index == nil {
		return "", ErrNilSessionIndex
	}
	if _, ambiguous := index.ambiguous[threadID]; ambiguous {
		return "", ErrAmbiguousRollout
	}
	path, exists := index.files[threadID]
	if !exists {
		return "", ErrRolloutNotFound
	}
	return path, nil
}

// Returns indexed child thread IDs in stable sorted order.
func (index *SessionIndex) ChildrenOf(parentID string) []string {
	if index == nil {
		return nil
	}
	return index.children.childrenOf(parentID)
}

// Returns verified indexed session IDs whose recorded working directory
// matches cwd. It does not scan rollout directories; metadata is captured as
// the index is populated.
func (index *SessionIndex) SessionIDsByCWD(cwd string) []string {
	if index == nil || strings.TrimSpace(cwd) == "" {
		return nil
	}
	wanted := filepath.Clean(strings.TrimSpace(cwd))
	if resolved, err := filepath.EvalSymlinks(wanted); err == nil {
		wanted = resolved
	}
	var matches []string
	for id, metadata := range index.metadata {
		if strings.TrimSpace(metadata.CWD) == "" {
			continue
		}
		metadataCWD := filepath.Clean(strings.TrimSpace(metadata.CWD))
		if resolved, err := filepath.EvalSymlinks(metadataCWD); err == nil {
			metadataCWD = resolved
		}
		_, ambiguous := index.ambiguous[id]
		if _, hasRollout := index.files[id]; hasRollout && !ambiguous && metadataCWD == wanted {
			matches = append(matches, id)
		}
	}
	slices.Sort(matches)
	return matches
}

// SessionByProcessStart selects one top-level session from indexed metadata.
// A second candidate is ambiguous; callers verify the selected rollout before use.
func (index *SessionIndex) SessionByProcessStart(ctx context.Context, cwd string, started time.Time, window time.Duration) (*SessionMetadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if index == nil {
		return nil, ErrNilSessionIndex
	}
	var match *SessionMetadata
	for _, id := range index.SessionIDsByCWD(cwd) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		metadata := index.metadata[id]
		if metadata.ParentThreadID != "" || metadata.ThreadSource == "subagent" || metadata.ThreadSource == "guardian_review" {
			continue
		}
		stamp, err := time.Parse(time.RFC3339Nano, metadata.Timestamp)
		if err != nil {
			continue
		}
		delta := stamp.Sub(started)
		if delta < -window || delta > window {
			continue
		}
		if match != nil {
			return nil, ErrAmbiguousRollout
		}
		match = &metadata
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if match == nil {
		return nil, ErrRolloutNotFound
	}
	return match, nil
}

// SessionIDsByTitle matches the exact Codex thread name and cwd encoded by
// Herdr's stripped terminal title: "thread name | directory basename".
func (index *SessionIndex) SessionIDsByTitle(title, cwd string) []string {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	suffix := " | " + filepath.Base(filepath.Clean(cwd))
	if !strings.HasSuffix(title, suffix) {
		return nil
	}
	name := strings.TrimSuffix(title, suffix)
	if name == "" {
		return nil
	}
	var matches []string
	for _, id := range index.SessionIDsByCWD(cwd) {
		if index.titles[id] == name {
			matches = append(matches, id)
		}
	}
	return matches
}

func (index *SessionIndex) populate(ctx context.Context, databases []*sql.DB) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, db := range databases {
		if db == nil {
			continue
		}
		if err := index.indexStateDatabase(ctx, db); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			index.addDiagnostic(fmt.Errorf("index Codex state database: %w", err))
		}
	}
	if index.edgesIndexed {
		return nil
	}

	candidates, err := index.metadataCandidates(ctx)
	if err != nil {
		return fmt.Errorf("find rollout metadata: %w", err)
	}
	for _, path := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		safe, err := ResolveRolloutPath(path, index.home)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrUnsafeRolloutPath) {
				continue
			}
			return err
		}
		metadata, err := ReadSessionMetadata(ctx, path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			var syntax *json.SyntaxError
			var fieldType *json.UnmarshalTypeError
			if !errors.As(err, &syntax) && !errors.As(err, &fieldType) {
				return err
			}
			index.addDiagnostic(err)
			continue
		}
		if metadata == nil || !ValidSessionID(metadata.ID) {
			continue
		}
		index.addRollout(metadata.ID, safe, metadata)
		if validSessionID.MatchString(metadata.ParentThreadID) {
			index.children.addEdge(metadata.ParentThreadID, metadata.ID)
		}
	}
	return nil
}

func (index *SessionIndex) addRollout(id, path string, metadata *SessionMetadata) {
	if existing, exists := index.files[id]; exists && existing != path {
		index.ambiguous[id] = struct{}{}
		delete(index.files, id)
		delete(index.metadata, id)
		return
	}
	if _, ambiguous := index.ambiguous[id]; ambiguous {
		return
	}
	index.files[id] = path
	if metadata != nil {
		index.metadata[id] = *metadata
	}
}

func (index *SessionIndex) metadataCandidates(ctx context.Context) ([]string, error) {
	if index.edgesIndexed {
		return nil, nil
	}

	if index.databaseSeen {
		candidates := make([]string, 0, len(index.files))
		for _, path := range index.files {
			candidates = append(candidates, path)
		}
		slices.Sort(candidates)
		return candidates, nil
	}

	var candidates []string
	err := walkRolloutFiles(ctx, index.home, func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidates = append(candidates, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(candidates)
	return candidates, nil
}

func (index *SessionIndex) indexStateDatabase(ctx context.Context, db *sql.DB) error {
	threadColumns, err := index.tableColumns(ctx, db, "threads")
	if err != nil {
		return err
	}
	_, hasThreadID := threadColumns["id"]
	_, hasRolloutPath := threadColumns["rollout_path"]
	if hasThreadID && hasRolloutPath {
		if err := index.readThreadRollouts(ctx, db, threadColumns); err != nil {
			return err
		}
		index.databaseSeen = true
	}

	edgeColumns, err := index.tableColumns(ctx, db, "thread_spawn_edges")
	if err != nil {
		return err
	}
	_, hasParentThreadID := edgeColumns["parent_thread_id"]
	_, hasChildThreadID := edgeColumns["child_thread_id"]
	if hasParentThreadID && hasChildThreadID {
		if err := index.readThreadEdges(ctx, db); err != nil {
			return err
		}
		index.databaseSeen = true
		index.edgesIndexed = true
	}
	return nil
}

func (index *SessionIndex) tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]struct{}, error) {
	// table comes from the fixed schema table names used above.
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]struct{})
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = struct{}{}
	}
	return columns, rows.Err()
}

func (index *SessionIndex) readThreadRollouts(ctx context.Context, db *sql.DB, columns map[string]struct{}) error {
	nameColumn, cwdColumn := "NULL", "NULL"
	if _, ok := columns["name"]; ok {
		nameColumn = "name"
	}
	if _, ok := columns["cwd"]; ok {
		cwdColumn = "cwd"
	}
	rows, err := db.QueryContext(ctx, "SELECT id, rollout_path, "+nameColumn+", "+cwdColumn+" FROM threads")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var threadID, rawPath, name, cwd sql.NullString
		if err := rows.Scan(&threadID, &rawPath, &name, &cwd); err != nil {
			return err
		}
		if !threadID.Valid || !rawPath.Valid || !validSessionID.MatchString(threadID.String) {
			continue
		}
		path, err := ResolveRolloutPath(rawPath.String, index.home)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrUnsafeRolloutPath) {
				continue
			}
			return err
		}
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		metadata, metadataErr := ReadSessionMetadata(ctx, path)
		if metadataErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			index.addDiagnostic(metadataErr)
		}
		if metadataErr != nil || metadata == nil || metadata.ID != threadID.String {
			metadata = nil
		}
		index.addRollout(threadID.String, path, metadata)
		if metadata != nil && name.Valid && cwd.Valid {
			actual, actualErr := filepath.EvalSymlinks(metadata.CWD)
			expected, expectedErr := filepath.EvalSymlinks(cwd.String)
			if actualErr != nil {
				actual = filepath.Clean(metadata.CWD)
			}
			if expectedErr != nil {
				expected = filepath.Clean(cwd.String)
			}
			if metadata.CWD != "" && cwd.String != "" && actual == expected {
				index.titles[threadID.String] = name.String
			}
		}
	}
	return rows.Err()
}

func (index *SessionIndex) readThreadEdges(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "SELECT parent_thread_id, child_thread_id FROM thread_spawn_edges")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var parentID, childID sql.NullString
		if err := rows.Scan(&parentID, &childID); err != nil {
			return err
		}
		if !parentID.Valid || !childID.Valid ||
			!validSessionID.MatchString(parentID.String) ||
			!validSessionID.MatchString(childID.String) {
			continue
		}
		index.children.addEdge(parentID.String, childID.String)
	}
	return rows.Err()
}
