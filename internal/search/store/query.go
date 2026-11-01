package store

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minTokenChars = 2  // --innodb-ft-min-token-size on both servers
	maxTokenChars = 84 // innodb_ft_max_token_size default
	maxQueryTerms = 8
)

// stopwords is MySQL's INFORMATION_SCHEMA.INNODB_FT_DEFAULT_STOPWORD. These words
// are never indexed, so a required term for one matches nothing and empties the
// whole query: "+how* +to* +deploy*" misses "how to deploy". The integration test
// compares this list with the server's.
var stopwords = map[string]struct{}{
	"a": {}, "about": {}, "an": {}, "are": {}, "as": {}, "at": {}, "be": {}, "by": {},
	"com": {}, "de": {}, "en": {}, "for": {}, "from": {}, "how": {}, "i": {}, "in": {},
	"is": {}, "it": {}, "la": {}, "of": {}, "on": {}, "or": {}, "that": {}, "the": {},
	"this": {}, "to": {}, "und": {}, "was": {}, "what": {}, "when": {}, "where": {},
	"who": {}, "will": {}, "with": {}, "www": {},
}

// booleanQuery turns user text into a MATCH ... IN BOOLEAN MODE expression that
// requires every word, each matched by prefix. Terms hold only letters, digits and
// underscores, so user text can't reach the boolean operators. It returns "" when
// no row can match.
func booleanQuery(text string) string {
	terms := make([]string, 0, maxQueryTerms)
	for token := range strings.FieldsFuncSeq(strings.ToLower(text), isSeparator) {
		chars := utf8.RuneCountInString(token)
		if chars < minTokenChars {
			continue
		}
		if _, stop := stopwords[token]; stop {
			continue
		}
		// MySQL silently ignores a longer term, which would stop requiring this
		// word. No indexed word can be that long, so nothing can match.
		if chars > maxTokenChars {
			return ""
		}
		term := "+" + token + "*"
		if slices.Contains(terms, term) {
			continue
		}
		terms = append(terms, term)
		if len(terms) == maxQueryTerms {
			break
		}
	}
	return strings.Join(terms, " ")
}

// isSeparator matches the default parser, which keeps underscores inside a word:
// "snake_case" is one index token, so splitting it would find nothing.
func isSeparator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}
