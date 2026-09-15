package index

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// Fuzzy supersession (SPEC 6.2): the conservative fallback for a memory written
// without a key that restates one already there in nearly the same words. Keys
// do the real work; this only catches a forgotten key on a near-verbatim
// restatement. Dice over character trigrams rates surface form, so it is kept
// on a short leash:
//
//   - Case, spacing and punctuation are removed first, so a restatement that
//     differs only in those scores exactly 1.
//   - Every number must match. Numbers carry the fact (a port, a version, a
//     count), and one changed digit barely moves the score.
//   - The default threshold is 0.95. "The frontend repo deploys to vercel on
//     every push" and the same sentence about the backend score about 0.9, and
//     they are two facts.

type fuzzyMatch struct {
	id    string
	score float64
}

// nearMissFloor is the score at or above which an unkeyed write that does not
// replace anything is reported as a near-miss candidate (SPEC 18): close
// enough to be the same fact the search before it failed to surface, not close
// enough to supersede. A starting guess, to tune once the metrics journal
// holds harvestable lines.
const nearMissFloor = 0.90

var digits = regexp.MustCompile(`[0-9]+`)

// fuzzyText is content as fuzzy matching compares it: lowercase letters and
// digits, single spaces between words.
func fuzzyText(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

func trigrams(text string) map[string]int {
	r := []rune(" " + text + " ")
	out := map[string]int{}
	for i := 0; i+3 <= len(r); i++ {
		out[string(r[i:i+3])]++
	}
	return out
}

func gramCount(g map[string]int) int {
	n := 0
	for _, c := range g {
		n += c
	}
	return n
}

// dice is the Dice coefficient of two trigram multisets: twice the shared
// trigrams over the total.
func dice(a, b map[string]int) float64 {
	na, nb := gramCount(a), gramCount(b)
	if na+nb == 0 {
		return 0
	}
	shared := 0
	for g, ca := range a {
		if cb := b[g]; cb > 0 {
			shared += min(ca, cb)
		}
	}
	return 2 * float64(shared) / float64(na+nb)
}

// fuzzyMatches returns the current unkeyed memories of the same scope and kind
// that content resembles, best match first, with score >= floor. Callers split
// at their own threshold: Write replaces anything at or above the fuzzy
// threshold and reports the band below it as near-misses (SPEC 6.2, 18).
func (ix *Index) fuzzyMatches(scope, kind, content string, floor float64) ([]fuzzyMatch, error) {
	text := fuzzyText(content)
	if text == "" {
		return nil, nil
	}
	rows, err := ix.db.Query(`SELECT id, content FROM memories
	                           WHERE scope=? AND kind=? AND key IS NULL AND valid_to IS NULL
	                           ORDER BY valid_from DESC`, scope, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	want, numbers := trigrams(text), digits.FindAllString(text, -1)
	nw := gramCount(want)
	var out []fuzzyMatch
	for rows.Next() {
		var id, other string
		if err := rows.Scan(&id, &other); err != nil {
			return nil, err
		}
		otherText := fuzzyText(other)
		if !slices.Equal(numbers, digits.FindAllString(otherText, -1)) {
			continue
		}
		g := trigrams(otherText)
		// Dice cannot exceed 2 x min / sum, so very different lengths are
		// skipped without comparing.
		ng := gramCount(g)
		if 2*float64(min(nw, ng))/float64(nw+ng) < floor {
			continue
		}
		if s := dice(want, g); s >= floor {
			out = append(out, fuzzyMatch{id: id, score: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out, rows.Err()
}
