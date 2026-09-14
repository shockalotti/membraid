package index

import (
	"slices"
	"sort"
	"strings"
)

// Key vocabulary (SPEC 6.3). Keys are chosen by agents, so they drift: one
// writes editor.theme, another theme, a third editor.themes, and three subjects
// quietly hold one fact. Normalization fixes punctuation, not wording. The cure
// is visibility, not machinery: the keys in use are listed with their counts,
// keys that look like one subject are paired up, and an agent about to start a
// new key that looks like an existing one is told so.

// KeyStat is one key in a scope: how many memories are current on it, and how
// many were ever written.
type KeyStat struct {
	Scope       string `json:"scope"`
	ScopeName   string `json:"scope_name,omitempty"`
	Kind        string `json:"kind"`
	Key         string `json:"key"`
	Current     int    `json:"current"`
	Total       int    `json:"total"`
	LastWritten string `json:"last_written"`
}

// KeyPair is two live keys in one scope and kind that probably name the same
// subject.
type KeyPair struct {
	Scope     string `json:"scope"`
	ScopeName string `json:"scope_name,omitempty"`
	Kind      string `json:"kind"`
	A         string `json:"a"`
	B         string `json:"b"`
}

// Keys lists every key in scope ("*" or "" for every scope, otherwise exactly
// that one), by scope, kind and key.
func (ix *Index) Keys(scope string) ([]KeyStat, error) {
	q := `SELECT scope, kind, key, SUM(valid_to IS NULL), COUNT(*), MAX(valid_from)
	        FROM memories WHERE key IS NOT NULL`
	var args []any
	if scope != "" && scope != "*" {
		q += ` AND scope = ?`
		args = append(args, scope)
	}
	q += ` GROUP BY scope, kind, key ORDER BY scope, kind, key`
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KeyStat
	for rows.Next() {
		var k KeyStat
		if err := rows.Scan(&k.Scope, &k.Kind, &k.Key, &k.Current, &k.Total, &k.LastWritten); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// KeyDrift pairs up live keys that look like one subject, within each scope
// and kind.
func (ix *Index) KeyDrift(scope string) ([]KeyPair, error) {
	keys, err := ix.Keys(scope)
	if err != nil {
		return nil, err
	}
	var out []KeyPair
	for i, a := range keys {
		if a.Current == 0 {
			continue
		}
		for _, b := range keys[i+1:] {
			if b.Scope != a.Scope || b.Kind != a.Kind {
				break // sorted by scope and kind
			}
			if b.Current > 0 && similarKeys(a.Key, b.Key) {
				out = append(out, KeyPair{Scope: a.Scope, Kind: a.Kind, A: a.Key, B: b.Key})
			}
		}
	}
	return out, nil
}

// SimilarKeys returns live keys like key, in scope and kind, and in shared as
// well when scope is a project, for a write that is about to start key as a
// new subject. It returns nothing when key is already in use there.
func (ix *Index) SimilarKeys(scope, kind, key string) ([]string, error) {
	key = NormalizeKey(key)
	if key == "" {
		return nil, nil
	}
	scopes := []string{scope}
	if scope != ScopeShared && scope != ScopeUnscoped {
		scopes = append(scopes, ScopeShared)
	}
	rows, err := ix.db.Query(`SELECT DISTINCT key FROM memories
	                           WHERE kind=? AND key IS NOT NULL AND valid_to IS NULL AND scope IN (`+placeholders(len(scopes))+`)`,
		append([]any{kind}, anySlice(scopes)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		if k == key {
			return nil, nil
		}
		if similarKeys(key, k) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, rows.Err()
}

func anySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

// similarKeys reports whether two different keys look like one subject:
//
//   - the same words in another order or run together: theme.editor,
//     editortheme and editor.theme;
//   - one is the end of the other: theme and editor.theme;
//   - nearly the same spelling: editor.theme and editor.themes.
func similarKeys(a, b string) bool {
	if a == b || a == "" || b == "" {
		return false
	}
	sa, sb := strings.Split(a, "."), strings.Split(b, ".")
	if strings.Join(sa, "") == strings.Join(sb, "") {
		return true
	}
	ss, sl := sa, sb
	if len(ss) > len(sl) {
		ss, sl = sl, ss
	}
	if len(ss) != len(sl) && slices.Equal(ss, sl[len(sl)-len(ss):]) {
		return true
	}
	if len(sa) == len(sb) {
		x, y := slices.Clone(sa), slices.Clone(sb)
		slices.Sort(x)
		slices.Sort(y)
		if slices.Equal(x, y) {
			return true
		}
	}
	return dice(trigrams(strings.Join(sa, " ")), trigrams(strings.Join(sb, " "))) >= 0.8
}
