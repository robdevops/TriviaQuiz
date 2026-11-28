package main

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// folds are the accented letters normalize writes plainly.
var folds = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a", 'ă': "a", 'ą': "a",
	'ç': "c", 'ć': "c", 'č': "c", 'ď': "d", 'đ': "d", 'ð': "d",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ė': "e", 'ę': "e", 'ě': "e",
	'ğ': "g", 'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'į': "i", 'ı': "i",
	'ł': "l", 'ľ': "l", 'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'ř': "r", 'ś': "s", 'š': "s", 'ş': "s", 'ș': "s", 'ť': "t", 'ţ': "t", 'ț': "t",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u", 'ų': "u",
	'ý': "y", 'ÿ': "y", 'ź': "z", 'ż': "z", 'ž': "z",
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'þ': "th",
}

// articles are the words normalize drops.
var articles = map[string]bool{"a": true, "an": true, "the": true}

// normalize is s as answers are compared: lower case, accents and
// punctuation gone, the words a, an and the dropped, single spaces.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch f, ok := folds[r]; {
		case ok:
			b.WriteString(f)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '’': // o'clock, don't
		default:
			b.WriteByte(' ')
		}
	}
	words := strings.Fields(b.String())
	kept := words[:0]
	for _, w := range words {
		if !articles[w] {
			kept = append(kept, w)
		}
	}
	return strings.Join(kept, " ")
}

// markText reports whether reply is one of a text question's accepted
// answers: normalised, then within an edit distance of 1 for an answer of
// 5 to 9 characters, or 2 for 10 or more. An answer with a number in it
// must match exactly.
func markText(reply string, accepted []string) bool {
	got := normalize(reply)
	if got == "" {
		return false
	}
	for _, a := range accepted {
		want := normalize(a)
		if want == "" {
			continue
		}
		if got == want || strings.ReplaceAll(got, " ", "") == strings.ReplaceAll(want, " ", "") { // a compound as one word or two: clownfish, clown fish
			return true
		}
		if strings.IndexFunc(want, unicode.IsDigit) >= 0 || strings.IndexFunc(got, unicode.IsDigit) >= 0 {
			continue
		}
		allowed := 0
		switch n := utf8.RuneCountInString(want); {
		case n >= 10:
			allowed = 2
		case n >= 5:
			allowed = 1
		}
		if allowed > 0 && distance(got, want, allowed) <= allowed {
			return true
		}
	}
	return false
}

// distance is the Levenshtein distance between a and b in runes, or
// limit+1 once it is surely over limit.
func distance(a, b string, limit int) int {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > limit || -d > limit {
		return limit + 1
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		best := cur[0]
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			best = min(best, cur[j])
		}
		if best > limit {
			return limit + 1
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// letter reads a reply to a choice question as a choice, 0 to 3 for A to
// D: the letter alone, in either case, with a bracket or full stop around
// it ("b", "B)", "(c)", "D."). Anything else is no answer.
func letter(reply string) (int, bool) {
	s := strings.TrimSpace(reply)
	s = strings.TrimPrefix(s, "(")
	s = strings.TrimRight(s, ").:! ")
	s = strings.TrimSpace(s)
	if len(s) != 1 {
		return 0, false
	}
	switch c := s[0] | 0x20; {
	case 'a' <= c && c <= 'd':
		return int(c - 'a'), true
	}
	return 0, false
}

// asking are the words of a question that only ask, never name part of
// the answer.
var asking = map[string]bool{}

func init() {
	for _, w := range strings.Fields("what which where when who whom whose why how type kind sort name named called call does did do is are was were this that these those with from into known of in on for by to and or its it") {
		asking[w] = true
	}
}

// withoutGiven is accepted with, for each answer, its form without the
// words the question already gives, so "Monster" is right for "Monster
// Beverage" when the question asks which beverage company, and "clown"
// for "clownfish" when it asks what type of fish. A word counts as given
// with or without a plural s; a compound loses a given word at its start
// or end. A form left with under 3 characters, or nothing, isn't added.
func withoutGiven(question string, accepted []string) []string {
	given := map[string]bool{}
	for _, w := range strings.Fields(normalize(question)) {
		if !asking[w] {
			given[w], given[strings.TrimSuffix(w, "s")] = true, true
		}
	}
	isGiven := func(w string) bool { return given[w] || given[strings.TrimSuffix(w, "s")] }
	out := slices.Clone(accepted)
	add := func(form string) {
		if utf8.RuneCountInString(strings.ReplaceAll(form, " ", "")) >= 3 && !slices.Contains(out, form) {
			out = append(out, form)
		}
	}
	for _, a := range accepted {
		words := strings.Fields(normalize(a))
		if len(words) > 1 {
			kept := slices.DeleteFunc(slices.Clone(words), isGiven)
			if len(kept) > 0 && len(kept) < len(words) {
				add(strings.Join(kept, " "))
			}
			continue
		}
		if len(words) == 1 {
			for g := range given {
				if len(g) < 3 || g == words[0] {
					continue
				}
				if rest, ok := strings.CutSuffix(words[0], g); ok {
					add(rest)
				}
				if rest, ok := strings.CutPrefix(words[0], g); ok {
					add(rest)
				}
			}
		}
	}
	return out
}
