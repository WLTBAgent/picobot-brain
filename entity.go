package brain

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Entity extraction patterns — work without LLM calls.
var (
	// [[Wikilinks]] — explicit entity references
	wikilinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

	// @mentions — person references
	mentionRe = regexp.MustCompile(`@(\w{2,30})`)

	// Email addresses — person references
	emailRe = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

	// "works at X", "CEO of X", "founded X" — relationship patterns
	worksAtRe = regexp.MustCompile(`(?i)(?:works?\s+at|employed\s+by|joined)\s+([A-Z][A-Za-z0-9&\s]{1,40})`)
	ceoOfRe   = regexp.MustCompile(`(?i)(?:CEO|CTO|CFO|COO|founder|co-founder)\s+(?:of\s+)?([A-Z][A-Za-z0-9&\s]{1,40})`)
	investedRe = regexp.MustCompile(`(?i)(?:invested\s+in|funded|backed)\s+([A-Z][A-Za-z0-9&\s]{1,40})`)
	attendedRe = regexp.MustCompile(`(?i)(?:attended|present\s+at|spoke\s+at)\s+([A-Z][A-Za-z0-9&\s]{1,50})`)
)

// ExtractEntities scans a page for entity references and creates entities/edges.
// This is the zero-LLM extraction path.
func (b *Brain) ExtractEntities(ctx context.Context, pageID int64) (int, error) {
	page, err := b.GetPageByID(ctx, pageID)
	if err != nil {
		return 0, err
	}

	created := 0
	content := page.Content

	// Extract [[wikilinks]]
	wikiMatches := wikilinkRe.FindAllStringSubmatch(content, -1)
	for _, m := range wikiMatches {
		name := strings.TrimSpace(m[1])
		entityType := inferEntityType(name, content)
		slug := "entities/" + slugify(name)
		if _, err := b.ensureEntity(ctx, page.SourceID, name, entityType, slug, &pageID); err == nil {
			created++
		}
	}

	// Extract @mentions as person entities
	mentionMatches := mentionRe.FindAllStringSubmatch(content, -1)
	for _, m := range mentionMatches {
		name := m[1]
		slug := "people/" + strings.ToLower(name)
		if _, err := b.ensureEntity(ctx, page.SourceID, name, "person", slug, &pageID); err == nil {
			created++
		}
	}

	// Extract relationship edges
	created += b.extractRelationships(ctx, page.SourceID, pageID, content, worksAtRe, "works_at")
	created += b.extractRelationships(ctx, page.SourceID, pageID, content, ceoOfRe, "leads")
	created += b.extractRelationships(ctx, page.SourceID, pageID, content, investedRe, "invested_in")
	created += b.extractRelationships(ctx, page.SourceID, pageID, content, attendedRe, "attended")

	return created, nil
}

// extractRelationships finds relationship patterns and creates entities + edges.
func (b *Brain) extractRelationships(ctx context.Context, sourceID string, pageID int64, content string, re *regexp.Regexp, edgeType string) int {
	created := 0
	matches := re.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		targetName := strings.TrimSpace(m[1])
		targetType := "company"
		if edgeType == "attended" {
			targetType = "event"
		}
		targetSlug := slugify(targetType) + "/" + slugify(targetName)

		targetID, err := b.ensureEntity(ctx, sourceID, targetName, targetType, targetSlug, nil)
		if err != nil {
			continue
		}

		// Create edge (source entity is the page's primary entity if it's a person page)
		// For now, we create a page-level edge
		_, err = b.db.Exec(`
			INSERT OR IGNORE INTO edges (from_id, to_id, type, source_page, confidence)
			SELECT ?, ?, ?, ?, 0.7`,
			pageID, targetID, edgeType, pageID)
		if err == nil {
			created++
		}
	}
	return created
}

// ensureEntity creates an entity if it doesn't exist, returns its ID.
func (b *Brain) ensureEntity(ctx context.Context, sourceID, name, entityType, slug string, pageID *int64) (int64, error) {
	// Try to find existing entity by slug
	var id int64
	err := b.db.QueryRow(`SELECT id FROM entities WHERE slug = ?`, slug).Scan(&id)
	if err == nil {
		return id, nil
	}

	// Create new entity
	var pid *int64
	if pageID != nil {
		pid = pageID
	} else {
		// Can't use nil directly in Scan-compatible way, use a workaround
	}

	res, err := b.db.Exec(`
		INSERT OR IGNORE INTO entities (source_id, name, type, slug, page_id)
		VALUES (?, ?, ?, ?, ?)`,
		sourceID, name, entityType, slug, pid)
	if err != nil {
		return 0, err
	}

	id, _ = res.LastInsertId()
	if id == 0 {
		b.db.QueryRow(`SELECT id FROM entities WHERE slug = ?`, slug).Scan(&id)
	}
	return id, nil
}

// GraphNeighbors returns entities connected to the given entity through edges.
func (b *Brain) GraphNeighbors(ctx context.Context, entityID int64, depth int) ([]Entity, []Edge, error) {
	if depth <= 0 {
		depth = 1
	}
	if depth > 3 {
		depth = 3 // prevent runaway traversal
	}

	visited := map[int64]bool{entityID: true}
	var allEntities []Entity
	var allEdges []Edge

	current := []int64{entityID}
	for d := 0; d < depth; d++ {
		var nextBatch []int64
		for _, eid := range current {
			// Find outgoing edges
			rows, err := b.db.Query(`
				SELECT e.id, e.from_id, e.to_id, e.type, e.source_page, e.confidence
				FROM edges e WHERE e.from_id = ?`, eid)
			if err != nil {
				continue
			}
			for rows.Next() {
				var edge Edge
				if rows.Scan(&edge.ID, &edge.FromID, &edge.ToID, &edge.Type, &edge.SourcePage, &edge.Confidence) != nil {
					continue
				}
				allEdges = append(allEdges, edge)
				if !visited[edge.ToID] {
					nextBatch = append(nextBatch, edge.ToID)
				}
			}
			rows.Close()

			// Find incoming edges
			rows, err = b.db.Query(`
				SELECT e.id, e.from_id, e.to_id, e.type, e.source_page, e.confidence
				FROM edges e WHERE e.to_id = ?`, eid)
			if err != nil {
				continue
			}
			for rows.Next() {
				var edge Edge
				if rows.Scan(&edge.ID, &edge.FromID, &edge.ToID, &edge.Type, &edge.SourcePage, &edge.Confidence) != nil {
					continue
				}
				allEdges = append(allEdges, edge)
				if !visited[edge.FromID] {
					nextBatch = append(nextBatch, edge.FromID)
				}
			}
			rows.Close()
		}

		// Load entity details for newly discovered entities
		for _, nid := range nextBatch {
			if visited[nid] {
				continue
			}
			visited[nid] = true
			entity, err := b.getEntityByID(ctx, nid)
			if err == nil {
				allEntities = append(allEntities, *entity)
			}
		}
		current = nextBatch
	}

	return allEntities, allEdges, nil
}

// FindEntities searches for entities by name or type.
func (b *Brain) FindEntities(ctx context.Context, query string, entityType string, limit int) ([]Entity, error) {
	if limit <= 0 {
		limit = 20
	}
	q := `SELECT id, source_id, name, type, slug, page_id FROM entities WHERE 1=1`
	args := []any{}
	if query != "" {
		q += ` AND name LIKE ?`
		args = append(args, "%"+query+"%")
	}
	if entityType != "" {
		q += ` AND type = ?`
		args = append(args, entityType)
	}
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := b.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []Entity
	for rows.Next() {
		var e Entity
		if rows.Scan(&e.ID, &e.SourceID, &e.Name, &e.Type, &e.Slug, &e.PageID) == nil {
			entities = append(entities, e)
		}
	}
	return entities, rows.Err()
}

func (b *Brain) getEntityByID(ctx context.Context, id int64) (*Entity, error) {
	var e Entity
	err := b.db.QueryRow(`SELECT id, source_id, name, type, slug, page_id FROM entities WHERE id = ?`, id).
		Scan(&e.ID, &e.SourceID, &e.Name, &e.Type, &e.Slug, &e.PageID)
	if err != nil {
		return nil, fmt.Errorf("entity not found: %w", err)
	}
	return &e, nil
}

// inferEntityType guesses entity type from name context.
func inferEntityType(name, content string) string {
	lower := strings.ToLower(content)
	switch {
	case strings.ContainsAny(lower, "company corp inc llc ltd"):
		return "company"
	case strings.ContainsAny(lower, "conference summit meetup event"):
		return "event"
	default:
		return "person"
	}
}
