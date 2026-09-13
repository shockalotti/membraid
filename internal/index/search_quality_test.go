package index

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The search evaluation's test set (docs/SEARCH-EVALUATION.md): 150 invented
// memories across six projects plus shared, and 100 queries in four kinds.
// paraphrase queries share no content word with their answer, so keyword
// search cannot find them by design; natural questions share some words;
// identifier queries name an env var, path, version or code; distractor
// queries have an obvious but wrong near-duplicate.
type searchMemory struct {
	ID, Kind, Key, Scope, Content string
}

type searchQuery struct {
	ID, Category, Query string
	Relevant            []string
}

func readJSONL[T any](t *testing.T, name string) []T {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "search", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out = append(out, v)
	}
	return out
}

// Keyword search must find ordinary questions and every exact identifier. When
// every word had to match, it found 15 of 100 in this set, all identifiers,
// and none of the 20 natural questions.
func TestKeywordSearchQuality(t *testing.T) {
	ix := newIndex(t)
	for _, m := range readJSONL[searchMemory](t, "memories.jsonl") {
		if _, err := ix.Write(Memory{ID: m.ID, Kind: m.Kind, Key: m.Key, Scope: m.Scope, Content: m.Content, Source: "testdata"}); err != nil {
			t.Fatalf("%s: %v", m.ID, err)
		}
	}

	found, total := map[string]int{}, map[string]int{}
	for _, q := range readJSONL[searchQuery](t, "queries.jsonl") {
		hits, err := ix.Search(q.Query, "*", 5)
		if err != nil {
			t.Fatalf("%s %q: %v", q.ID, q.Query, err)
		}
		relevant := map[string]bool{}
		for _, id := range q.Relevant {
			relevant[id] = true
		}
		total[q.Category]++
		total["overall"]++
		for _, h := range hits {
			if relevant[h.ID] {
				found[q.Category]++
				found["overall"]++
				break
			}
		}
	}

	recall := func(c string) float64 { return float64(found[c]) / float64(total[c]) }
	for _, c := range []string{"overall", "natural", "identifier", "distractor", "paraphrase"} {
		t.Logf("recall@5 %-10s %.2f (%d/%d)", c, recall(c), found[c], total[c])
	}

	// Floors sit just under what the evaluation measured (overall 0.47,
	// natural 0.95, identifier 1.00, distractor 0.87), so a regression fails
	// and noise from a tie in bm25 does not.
	floors := map[string]float64{"overall": 0.45, "natural": 0.90, "identifier": 1.00, "distractor": 0.80}
	for c, floor := range floors {
		if recall(c) < floor {
			t.Errorf("recall@5 for %s queries is %.2f, below %.2f", c, recall(c), floor)
		}
	}
}

func TestFTSQuery(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"What does the billing API deploy to?", `"billing" OR "api" OR "deploy"`},
		{"LEDGER_PG_DSN", `"ledger_pg_dsn"`},
		{"deploys to fly.io", `"deploys" OR "fly.io"`},
		{"error LL-4091 in src/app.ts", `"error" OR "ll-4091" OR "src/app.ts"`},
		{`say "hello" twice twice`, `"say" OR "hello" OR "twice"`},
		{"what is it", `""`},
		{"   ", `""`},
	} {
		if got := ftsQuery(tc.in); got != tc.want {
			t.Errorf("ftsQuery(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}
