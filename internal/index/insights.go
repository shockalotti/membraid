package index

import (
	"os"
	"sort"
	"strings"
)

// ProjectInfo is one project as the widget's Projects tab shows it: what is
// remembered there, what is open, when it was last written to and by which
// agents, and where its folder is on this machine.
type ProjectInfo struct {
	Scope     string   `json:"scope"`
	Name      string   `json:"name"`
	Path      string   `json:"path,omitempty"`
	Missing   bool     `json:"missing,omitempty"`
	Memories  int      `json:"memories"`
	OpenTasks int      `json:"open_tasks"`
	LastWrite string   `json:"last_write,omitempty"`
	Sources   []string `json:"sources"`
}

// Projects lists every project that has memories or has been seen on this
// machine, shared first, then the most recently written to.
func (ix *Index) Projects() ([]ProjectInfo, error) {
	rows, err := ix.db.Query(`
		WITH ids AS (SELECT scope FROM memories UNION SELECT scope FROM scopes)
		SELECT ids.scope, COALESCE(s.name, ''), COALESCE(s.path, ''),
		  (SELECT COUNT(*) FROM memories m WHERE m.scope = ids.scope AND m.valid_to IS NULL),
		  (SELECT COUNT(*) FROM memories m WHERE m.scope = ids.scope AND m.valid_to IS NULL AND m.kind = ?),
		  COALESCE((SELECT MAX(valid_from) FROM memories m WHERE m.scope = ids.scope), ''),
		  COALESCE((SELECT GROUP_CONCAT(DISTINCT source) FROM memories m WHERE m.scope = ids.scope), '')
		FROM ids LEFT JOIN scopes s ON s.scope = ids.scope`, KindTaskState)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		var sources string
		if err := rows.Scan(&p.Scope, &p.Name, &p.Path, &p.Memories, &p.OpenTasks, &p.LastWrite, &sources); err != nil {
			return nil, err
		}
		if p.Scope == ScopeShared {
			p.Name = ScopeShared
		} else if p.Name == "" {
			p.Name = p.Scope
		}
		if p.Path != "" {
			if _, err := os.Stat(p.Path); err != nil {
				p.Missing = true
			}
		}
		p.Sources = []string{}
		if sources != "" {
			p.Sources = strings.Split(sources, ",")
			sort.Strings(p.Sources)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Scope == ScopeShared) != (out[j].Scope == ScopeShared) {
			return out[i].Scope == ScopeShared
		}
		if out[i].LastWrite != out[j].LastWrite {
			return out[i].LastWrite > out[j].LastWrite
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// PruneScopes forgets projects this machine has seen that have never held a
// memory: a folder an agent passed through once, a temp directory. Only the
// local registry changes; nothing is written to the log, since there is
// nothing to tell other machines.
func (ix *Index) PruneScopes() (int, error) {
	res, err := ix.db.Exec(`DELETE FROM scopes WHERE scope NOT IN (SELECT DISTINCT scope FROM memories)`)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// DayCount is the number of memories written on one day (UTC).
type DayCount struct {
	Day   string `json:"day"`
	Count int    `json:"count"`
}

// Insights is a first, small look at how memory is being used.
type Insights struct {
	Days           int            `json:"days"`
	WritesBySource map[string]int `json:"writes_by_source"`
	WritesByDay    []DayCount     `json:"writes_by_day"`
	Current        int            `json:"current"`
	NeverUsed      int            `json:"never_used"`
	RecentlyUsed   []Hit          `json:"recently_used"`
}

// Insights covers the last days days, today included. Writes count every
// statement made in the window, including ones since replaced: they show which
// agents are writing at all, which is how a harness whose setup broke shows up.
func (ix *Index) Insights(days int) (*Insights, error) {
	if days <= 0 {
		days = 7
	}
	today := ix.now().UTC()
	from := today.AddDate(0, 0, -(days - 1)).Format("2006-01-02")
	in := &Insights{Days: days, WritesBySource: map[string]int{}, RecentlyUsed: []Hit{}}

	counts := map[string]int{}
	rows, err := ix.db.Query(`
		SELECT substr(valid_from, 1, 10) AS day, source, COUNT(*) FROM memories
		 WHERE substr(valid_from, 1, 10) >= ? GROUP BY day, source`, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day, source string
		var n int
		if err := rows.Scan(&day, &source, &n); err != nil {
			rows.Close()
			return nil, err
		}
		counts[day] += n
		in.WritesBySource[source] += n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := days - 1; i >= 0; i-- {
		day := today.AddDate(0, 0, -i).Format("2006-01-02")
		in.WritesByDay = append(in.WritesByDay, DayCount{Day: day, Count: counts[day]})
	}

	if err := ix.db.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE last_retrieved IS NULL) FROM memories WHERE valid_to IS NULL`).
		Scan(&in.Current, &in.NeverUsed); err != nil {
		return nil, err
	}
	used, err := ix.hits(`SELECT id, kind, key, content, scope, source, last_retrieved FROM memories
		WHERE valid_to IS NULL AND last_retrieved IS NOT NULL ORDER BY last_retrieved DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	if used != nil {
		in.RecentlyUsed = used
	}
	return in, nil
}

// Browse lists current memories newest first, for reading rather than for an
// agent: nothing is marked as retrieved. scope "" or "*" means every project,
// otherwise exactly that scope; kind and source filter when set.
func (ix *Index) Browse(scope, kind, source string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, kind, COALESCE(key, ''), content, scope, source, valid_from, COALESCE(source_concept, '') FROM memories WHERE valid_to IS NULL`
	var args []any
	for _, f := range []struct{ col, val string }{{"scope", scope}, {"kind", kind}, {"source", source}} {
		if f.val != "" && f.val != "*" {
			q += ` AND ` + f.col + ` = ?`
			args = append(args, f.val)
		}
	}
	q += ` ORDER BY valid_from DESC LIMIT ?`
	args = append(args, limit)
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ID, &h.Kind, &h.Key, &h.Content, &h.Scope, &h.Source, &h.At, &h.Concept); err != nil {
			return nil, err
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}
