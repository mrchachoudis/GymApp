package llm

import (
	"regexp"
	"strings"
	"unicode"
)

// Post-processing exists because prompt rules are best-effort and open models
// leak. Anything that can be enforced deterministically in Go is enforced
// here rather than hoped for in the system prompt.

var reWhitespaceRun = regexp.MustCompile(`[ \t]{2,}`)

// StripEmoji removes emoji and pictographic characters from a coach reply.
// The prompt asks for no emoji; this guarantees it.
func StripEmoji(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isEmoji(r) {
			continue
		}
		b.WriteRune(r)
	}
	out := reWhitespaceRun.ReplaceAllString(b.String(), " ")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F300 && r <= 0x1FAFF, // pictographs, emoticons, symbols
		r >= 0x1F000 && r <= 0x1F2FF, // mahjong, playing cards, enclosed
		r >= 0x2600 && r <= 0x27BF,   // misc symbols and dingbats
		r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
		r >= 0x1F1E6 && r <= 0x1F1FF, // regional indicators
		r == 0x200D,                  // zero-width joiner
		r >= 0x2190 && r <= 0x21FF,   // arrows
		r >= 0x2B00 && r <= 0x2BFF:   // misc symbols and arrows
		return true
	}
	return unicode.Is(unicode.So, r)
}

// bannedMetaphors are the words that mean the reply reached for an aquatic
// metaphor. Mario studies ichthyology; the prompt forbids it, and this catches
// the leaks. A hit triggers exactly one regeneration, then the reply ships
// anyway rather than looping.
var bannedMetaphors = []string{
	"fish", "ocean", "marine", "aquatic", "swim", "swimming", "swam",
	"tide", "tidal", "shark", "whale", "dive into", "diving into",
	"sink or swim", "make waves", "riptide", "undertow", "school of",
	"reel in", "reeling in", "hook, line", "bigger pond", "gills",
	"aquarium", "seafloor", "deep end",
}

// openers the prompt bans. Cheaper to detect and regenerate than to trust.
var bannedOpeners = []string{
	"great job", "amazing work", "nice session", "awesome work",
	"fantastic", "well done", "keep up the", "great work", "good job",
}

// Violation describes why a reply failed post-processing.
type Violation struct {
	Kind string // "metaphor" or "opener"
	Term string
}

// CheckReply reports the first rule the reply breaks, if any. Word-boundary
// matching keeps "diving into" from firing on unrelated substrings.
func CheckReply(s string) (Violation, bool) {
	lower := strings.ToLower(s)

	for _, term := range bannedMetaphors {
		if containsWord(lower, term) {
			return Violation{Kind: "metaphor", Term: term}, true
		}
	}

	firstLine := lower
	if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
		firstLine = firstLine[:nl]
	}
	for _, term := range bannedOpeners {
		if strings.HasPrefix(strings.TrimLeft(firstLine, "\"' "), term) {
			return Violation{Kind: "opener", Term: term}, true
		}
	}
	return Violation{}, false
}

// containsWord matches a term on word boundaries so "swim" does not fire on
// "swimwear" and, more importantly, so multi-word terms still match.
func containsWord(haystack, needle string) bool {
	if strings.ContainsAny(needle, " ,") {
		return strings.Contains(haystack, needle)
	}
	for i := 0; ; {
		idx := strings.Index(haystack[i:], needle)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(needle)
		beforeOK := start == 0 || !isWordRune(rune(haystack[start-1]))
		afterOK := end == len(haystack) || !isWordRune(rune(haystack[end]))
		if beforeOK && afterOK {
			return true
		}
		i = start + 1
		if i >= len(haystack) {
			return false
		}
	}
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
