package codex

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// SessionIndex maps Codex thread IDs to rollout files and tracks child threads.
type SessionIndex struct {
	db           queryer
	home         string
	files        map[string]string
	children     sessionGraph
	databaseSeen bool
	edgesIndexed bool
}

// NewSessionIndex builds an index using a caller-owned, read-only database.
// The caller remains responsible for closing db.
func NewSessionIndex(ctx context.Context, db *sql.DB, codexHome string) (*SessionIndex, error) {
	if db == nil {
		return nil, fmt.Errorf("Codex state database is nil")
	}
	if codexHome == "" {
		return nil, fmt.Errorf("Codex home is empty")
	}

	index := &SessionIndex{
		db:       db,
		home:     codexHome,
		files:    make(map[string]string),
		children: sessionGraph{},
	}
	if err := index.populate(ctx); err != nil {
		return nil, err
	}
	return index, nil
}

// RolloutPath returns the indexed rollout path for a thread, if present.
func (index *SessionIndex) RolloutPath(threadID string) (string, bool) {
	if index == nil {
		return "", false
	}
	path, ok := index.files[threadID]
	return path, ok
}

// ChildrenOf returns the indexed child thread IDs in stable sorted order.
func (index *SessionIndex) ChildrenOf(parentID string) []string {
	if index == nil {
		return nil
	}
	return index.children.childrenOf(parentID)
}

func (index *SessionIndex) populate(ctx context.Context) error {
	if err := index.indexStateDatabase(ctx); err != nil {
		return fmt.Errorf("index Codex state database: %w", err)
	}
	if index.edgesIndexed {
		return nil
	}

	candidates, err := index.metadataCandidates()
	if err != nil {
		return fmt.Errorf("find rollout metadata: %w", err)
	}
	for _, path := range candidates {
		metadata, err := readSessionMetadata(path)
		if err != nil || metadata == nil ||
			!validSessionID.MatchString(metadata.ID) ||
			!validSessionID.MatchString(metadata.ParentThreadID) {
			continue
		}

		safe, ok := isSafePath(path, index.home)
		if !ok {
			continue
		}
		index.files[metadata.ID] = safe
		index.children.addEdge(metadata.ParentThreadID, metadata.ID)
	}
	return nil
}

func (index *SessionIndex) metadataCandidates() ([]string, error) {
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
	err := walkRolloutFiles(index.home, func(path string) (bool, error) {
		candidates = append(candidates, path)
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(candidates)
	return candidates, nil
}

func (index *SessionIndex) indexStateDatabase(ctx context.Context) error {
	threadColumns, err := index.tableColumns(ctx, "threads")
	if err != nil {
		return err
	}
	if threadColumns["id"] && threadColumns["rollout_path"] {
		index.databaseSeen = true
		if err := index.readThreadRollouts(ctx); err != nil {
			return err
		}
	}

	edgeColumns, err := index.tableColumns(ctx, "thread_spawn_edges")
	if err != nil {
		return err
	}
	if edgeColumns["parent_thread_id"] && edgeColumns["child_thread_id"] {
		index.databaseSeen = true
		index.edgesIndexed = true
		if err := index.readThreadEdges(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (index *SessionIndex) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	// table comes from the fixed schema table names used above.
	rows, err := index.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

func (index *SessionIndex) readThreadRollouts(ctx context.Context) error {
	rows, err := index.db.QueryContext(ctx, "SELECT id, rollout_path FROM threads")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var threadID, rawPath sql.NullString
		if err := rows.Scan(&threadID, &rawPath); err != nil {
			return err
		}
		if !threadID.Valid || !rawPath.Valid || !validSessionID.MatchString(threadID.String) {
			continue
		}
		path, ok := isSafePath(rawPath.String, index.home)
		if !ok {
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		index.files[threadID.String] = path
	}
	return rows.Err()
}

func (index *SessionIndex) readThreadEdges(ctx context.Context) error {
	rows, err := index.db.QueryContext(ctx, "SELECT parent_thread_id, child_thread_id FROM thread_spawn_edges")
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
