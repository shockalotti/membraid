package index

import "database/sql"

// SourceKeyPrefix marks a knowledge location: a memory saying where knowledge
// lives outside membraid (a folder of specs, notes or docs) so agents read it
// there. membraid points to the folder; it never reads what is inside.
const SourceKeyPrefix = "source."

// Sources lists current knowledge locations, newest first. scope "" or "*"
// means every project; anything else is exactly that scope.
func (ix *Index) Sources(scope string) ([]Hit, error) {
	q := `SELECT id, kind, key, content, scope, source, valid_from FROM memories
	       WHERE valid_to IS NULL AND substr(key, 1, ?) = ?`
	args := []any{len(SourceKeyPrefix), SourceKeyPrefix}
	if scope != "" && scope != "*" {
		q += ` AND scope = ?`
		args = append(args, scope)
	}
	q += ` ORDER BY valid_from DESC`
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Hit{}
	for rows.Next() {
		var h Hit
		var key sql.NullString
		if err := rows.Scan(&h.ID, &h.Kind, &key, &h.Content, &h.Scope, &h.Source, &h.At); err != nil {
			return nil, err
		}
		h.Key = key.String
		out = append(out, h)
	}
	return out, rows.Err()
}
