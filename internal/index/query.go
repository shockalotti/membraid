package index

import (
	"strings"
	"unicode"
)

// ftsQuery turns what an agent types into an FTS5 MATCH expression.
//
// Agents search with questions ("what does the billing API deploy to?"), and
// most of those words appear in no memory. Requiring every word to match, as
// this did at first, found nothing for ordinary questions: 0 of 85 in the
// search evaluation, against 15 of 15 exact identifiers (docs/SEARCH-EVALUATION.md).
// So common words are dropped, and any remaining term may match, with bm25
// ranking memories that match more and rarer terms first.
//
// A token that looks like an identifier (LEDGER_PG_DSN, src/app.ts, go1.23.4,
// LL-4091, fly.io) is kept whole as one quoted string, which FTS5 matches as a
// phrase of its parts. Splitting it would let "go" or "src" match everything.
//
// Returns `""`, which matches nothing, when no term survives.
func ftsQuery(q string) string {
	seen := map[string]bool{}
	var terms []string
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			terms = append(terms, `"`+strings.ReplaceAll(t, `"`, `""`)+`"`)
		}
	}
	for _, field := range strings.Fields(strings.ToLower(q)) {
		core := strings.TrimFunc(field, notAlnum)
		if core == "" {
			continue
		}
		if isIdentifier(core) {
			add(core)
			continue
		}
		for _, w := range strings.FieldsFunc(core, notAlnum) {
			if !stopwords[w] {
				add(w)
			}
		}
	}
	if len(terms) == 0 {
		return `""`
	}
	return strings.Join(terms, " OR ")
}

func notAlnum(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }

// identifierSeparators mark a token as an identifier when they sit between
// letters or digits inside it.
const identifierSeparators = "_./-:@+=~"

func isIdentifier(core string) bool {
	rs := []rune(core)
	for i := 1; i < len(rs)-1; i++ {
		if strings.ContainsRune(identifierSeparators, rs[i]) {
			return true
		}
	}
	return false
}

// stopwords is the NLTK English stopword list (nltk_data corpora/stopwords
// "english", 179 words). Contractions cannot match after splitting on
// non-alphanumerics; their fragments (don, t, ll, ve) are listed themselves.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`i me my myself we our ours ourselves you you're you've you'll you'd
	your yours yourself yourselves he him his himself she she's her hers herself it it's its itself they
	them their theirs themselves what which who whom this that that'll these those am is are was were be
	been being have has had having do does did doing a an the and but if or because as until while of at
	by for with about against between into through during before after above below to from up down in
	out on off over under again further then once here there when where why how all any both each few
	more most other some such no nor not only own same so than too very s t can will just don don't
	should should've now d ll m o re ve y ain aren aren't couldn couldn't didn didn't doesn doesn't hadn
	hadn't hasn hasn't haven haven't isn isn't ma mightn mightn't mustn mustn't needn needn't shan shan't
	shouldn shouldn't wasn wasn't weren weren't won won't wouldn wouldn't`) {
		m[w] = true
	}
	return m
}()
