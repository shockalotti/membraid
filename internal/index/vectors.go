package index

import (
	"container/heap"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"sort"
)

// Vector search, as chosen by measurement (docs/SEARCH-EVALUATION.md): full
// float32 vectors and one sign bit per dimension live in the ordinary SQLite
// index. A search ranks every in-scope row by Hamming distance on the bits,
// keeps the best vectorCandidates, then rescores those exactly by cosine. On
// real embeddings that returned 0.9975 of the exact top 10 at 100k rows and
// 0.997 at 1M.
//
// Short CLI processes scan the bits through SQL. Long-lived processes (MCP
// servers) call EnableVectorCache and scan bits held in memory, reloading only
// when vector_gen shows something changed.

const vectorCandidates = 200

// PutVector stores a memory's embedding for one model, replacing any earlier
// one. The vector is normalised here, so cosine is a dot product.
func (ix *Index) PutVector(id, model string, v []float32) error {
	if len(v) == 0 {
		return fmt.Errorf("index: empty vector for %s", id)
	}
	n := normalised(v)
	_, err := ix.db.Exec(`INSERT INTO vectors (id, model, full, bits) VALUES (?,?,?,?)
	                      ON CONFLICT(id, model) DO UPDATE SET full=excluded.full, bits=excluded.bits`,
		id, model, encodeFull(n), signBits(n))
	return err
}

// Pending is a current memory with no vector yet for some model.
type Pending struct {
	ID, Content string
}

// MissingVectors lists current memories that have no vector for model, newest
// first, so the most recent memories become searchable first.
func (ix *Index) MissingVectors(model string, limit int) ([]Pending, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := ix.db.Query(`SELECT m.id, m.content FROM memories m
	                           WHERE m.valid_to IS NULL
	                             AND NOT EXISTS (SELECT 1 FROM vectors v WHERE v.id = m.id AND v.model = ?)
	                           ORDER BY m.valid_from DESC LIMIT ?`, model, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Content); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// VectorCoverage reports how many current memories have a vector for model.
func (ix *Index) VectorCoverage(model string) (have, total int, err error) {
	err = ix.db.QueryRow(`SELECT
	        (SELECT COUNT(*) FROM memories m WHERE m.valid_to IS NULL
	           AND EXISTS (SELECT 1 FROM vectors v WHERE v.id = m.id AND v.model = ?)),
	        (SELECT COUNT(*) FROM memories WHERE valid_to IS NULL)`, model).Scan(&have, &total)
	return have, total, err
}

// DropOtherVectors deletes vectors made by any model other than model. After a
// change of embedder the old vectors can never be compared with new queries,
// so they are only dead weight. Returns how many were removed.
func (ix *Index) DropOtherVectors(model string) (int, error) {
	res, err := ix.db.Exec(`DELETE FROM vectors WHERE model <> ?`, model)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// EnableVectorCache keeps sign bits in memory for this process. Worth it for a
// process that searches many times; a single CLI call should not pay the load.
func (ix *Index) EnableVectorCache() {
	ix.vmu.Lock()
	ix.vcacheOn = true
	ix.vmu.Unlock()
}

// VectorSearch returns the current memories nearest to q among those in scope
// that have a vector for model, best first. Scores are cosine similarity times
// the same decay keyword search applies.
func (ix *Index) VectorSearch(q []float32, model, scope string, limit int) ([]Hit, error) {
	if limit <= 0 {
		limit = 10
	}
	if len(q) == 0 {
		return nil, fmt.Errorf("index: empty query vector")
	}
	qn := normalised(q)
	qbits := words(signBits(qn))

	ix.vmu.Lock()
	useCache := ix.vcacheOn
	ix.vmu.Unlock()

	var cands []string
	var err error
	if useCache {
		cands, err = ix.cachedCandidates(qbits, model, scope)
	} else {
		cands, err = ix.sqlCandidates(qbits, model, scope)
	}
	if err != nil || len(cands) == 0 {
		return nil, err
	}
	return ix.rescore(qn, model, cands, limit)
}

func (ix *Index) sqlCandidates(qbits []uint64, model, scope string) ([]string, error) {
	q := `SELECT v.id, v.bits FROM vectors v JOIN memories m ON m.id = v.id
	       WHERE v.model = ? AND m.valid_to IS NULL`
	args := []any{model}
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		q += ` AND m.scope IN (` + placeholders(len(scopes)) + `)`
		for _, s := range scopes {
			args = append(args, s)
		}
	}
	rows, err := ix.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("index: vector scan: %w", err)
	}
	defer rows.Close()
	best := &nearest{k: vectorCandidates}
	var id string
	var b sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		if len(b) != 8*len(qbits) {
			continue // a vector of another dimension: not comparable
		}
		best.offer(id, -float64(hamming(qbits, b)))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return best.ids(), nil
}

// vectorCache is the in-memory form of every current vector for one model.
type vectorCache struct {
	model  string
	gen    string
	ids    []string
	scopes []string
	nw     int
	bits   []uint64
}

func (ix *Index) cachedCandidates(qbits []uint64, model, scope string) ([]string, error) {
	c, err := ix.loadVectorCache(model)
	if err != nil {
		return nil, err
	}
	if c.nw != len(qbits) && len(c.ids) > 0 {
		return nil, nil
	}
	var allowed map[string]bool
	if scopes := effectiveScopes(scope); len(scopes) > 0 {
		allowed = map[string]bool{}
		for _, s := range scopes {
			allowed[s] = true
		}
	}
	best := &nearest{k: vectorCandidates}
	for i, id := range c.ids {
		if allowed != nil && !allowed[c.scopes[i]] {
			continue
		}
		row := c.bits[i*c.nw : (i+1)*c.nw]
		d := 0
		for j, w := range qbits {
			d += bits.OnesCount64(w ^ row[j])
		}
		best.offer(id, -float64(d))
	}
	return best.ids(), nil
}

// loadVectorCache returns the cache for model, reloading it when vector_gen has
// moved since it was built. The generation is read before loading, so a change
// that lands during the load triggers another reload next time rather than
// being missed.
func (ix *Index) loadVectorCache(model string) (*vectorCache, error) {
	gen := ix.metaGet("vector_gen")
	ix.vmu.Lock()
	defer ix.vmu.Unlock()
	if c := ix.vcache; c != nil && c.model == model && c.gen == gen {
		return c, nil
	}
	rows, err := ix.db.Query(`SELECT v.id, m.scope, v.bits FROM vectors v JOIN memories m ON m.id = v.id
	                           WHERE v.model = ? AND m.valid_to IS NULL`, model)
	if err != nil {
		return nil, fmt.Errorf("index: load vectors: %w", err)
	}
	defer rows.Close()
	c := &vectorCache{model: model, gen: gen}
	var id, sc string
	var b sql.RawBytes
	for rows.Next() {
		if err := rows.Scan(&id, &sc, &b); err != nil {
			return nil, err
		}
		if c.nw == 0 {
			c.nw = len(b) / 8
		}
		if len(b) != 8*c.nw {
			continue
		}
		c.ids = append(c.ids, id)
		c.scopes = append(c.scopes, sc)
		for i := 0; i < c.nw; i++ {
			c.bits = append(c.bits, binary.LittleEndian.Uint64(b[8*i:]))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ix.vcache = c
	return c, nil
}

// rescore loads the candidates' full vectors and memory rows, scores them by
// cosine times decay, and returns the best limit as hits.
func (ix *Index) rescore(q []float32, model string, cands []string, limit int) ([]Hit, error) {
	args := []any{model}
	for _, id := range cands {
		args = append(args, id)
	}
	rows, err := ix.db.Query(`SELECT m.id, m.kind, m.key, m.content, m.scope, m.source, m.valid_from, m.last_retrieved, v.full
	                            FROM vectors v JOIN memories m ON m.id = v.id
	                           WHERE v.model = ? AND m.valid_to IS NULL AND v.id IN (`+placeholders(len(cands))+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("index: vector rescore: %w", err)
	}
	defer rows.Close()
	var hits []Hit
	var relevance []float64
	for rows.Next() {
		var h Hit
		var key, retrieved sql.NullString
		var full []byte
		if err := rows.Scan(&h.ID, &h.Kind, &key, &h.Content, &h.Scope, &h.Source, &h.At, &retrieved, &full); err != nil {
			return nil, err
		}
		if len(full) != 4*len(q) {
			continue
		}
		h.Key = key.String
		hits = append(hits, h)
		relevance = append(relevance, float64(dot(q, full)))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	// A stable order for equal scores, whatever order the rows came in.
	order := make([]int, len(hits))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return hits[order[a]].ID < hits[order[b]].ID })
	sortedHits := make([]Hit, len(hits))
	sortedRel := make([]float64, len(hits))
	for i, k := range order {
		sortedHits[i], sortedRel[i] = hits[k], relevance[k]
	}
	return ix.rankByUse(sortedHits, sortedRel, limit)
}

func normalised(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if sum == 0 {
		copy(out, v)
		return out
	}
	inv := 1 / math.Sqrt(sum)
	for i, x := range v {
		out[i] = float32(float64(x) * inv)
	}
	return out
}

func encodeFull(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// signBits packs one sign bit per dimension, padded to whole 64-bit words.
func signBits(v []float32) []byte {
	b := make([]byte, (len(v)+63)/64*8)
	for i, x := range v {
		if x > 0 {
			b[i/8] |= 1 << (i % 8)
		}
	}
	return b
}

func words(b []byte) []uint64 {
	w := make([]uint64, len(b)/8)
	for i := range w {
		w[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	return w
}

func hamming(q []uint64, b []byte) int {
	d := 0
	for i, w := range q {
		d += bits.OnesCount64(w ^ binary.LittleEndian.Uint64(b[8*i:]))
	}
	return d
}

// dot is cosine similarity for normalised vectors; b is an encoded full vector.
func dot(q []float32, b []byte) float64 {
	var s float64
	for i, x := range q {
		s += float64(x) * float64(math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:])))
	}
	return s
}

// nearest keeps the k highest scores offered (a min-heap on score). Ties break
// by id so results do not depend on scan order.
type nearest struct {
	k     int
	items []scoredID
}

type scoredID struct {
	id    string
	score float64
}

func (n *nearest) Len() int { return len(n.items) }
func (n *nearest) Less(i, j int) bool {
	if n.items[i].score != n.items[j].score {
		return n.items[i].score < n.items[j].score
	}
	return n.items[i].id > n.items[j].id
}
func (n *nearest) Swap(i, j int) { n.items[i], n.items[j] = n.items[j], n.items[i] }
func (n *nearest) Push(x any)    { n.items = append(n.items, x.(scoredID)) }
func (n *nearest) Pop() any {
	x := n.items[len(n.items)-1]
	n.items = n.items[:len(n.items)-1]
	return x
}

func (n *nearest) offer(id string, score float64) {
	s := scoredID{id, score}
	if len(n.items) < n.k {
		heap.Push(n, s)
		return
	}
	if top := n.items[0]; score > top.score || (score == top.score && id < top.id) {
		n.items[0] = s
		heap.Fix(n, 0)
	}
}

func (n *nearest) ids() []string {
	out := make([]string, len(n.items))
	for i, s := range n.items {
		out[i] = s.id
	}
	return out
}
