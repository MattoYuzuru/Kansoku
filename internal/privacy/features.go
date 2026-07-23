package privacy

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExtractPromptFeatures computes the closed PromptFeatures shape for one raw
// prompt string, in memory only. It is exported so any other package that
// needs to compute the identical, already-audited feature shape (for example
// internal/codexadapter's hook helper, which must derive prompt features
// in-process without ever persisting the raw prompt) reuses this single
// implementation instead of hand-rolling a second one; it is not a second
// sanitizer, only the one feature-extraction routine internal/privacy's own
// DecodeAndExtract already uses.
func ExtractPromptFeatures(prompt string, attachmentCount int) PromptFeatures {
	features := PromptFeatures{
		State:              CompletenessComplete,
		ByteCount:          len([]byte(prompt)),
		CharacterCount:     utf8.RuneCountInString(prompt),
		LineCount:          0,
		CoarseScript:       "unknown",
		CodeFenceCount:     strings.Count(prompt, "```") / 2,
		AttachmentCount:    attachmentCount,
		URLReferenceCount:  strings.Count(prompt, "https://") + strings.Count(prompt, "http://"),
		FileReferenceCount: countFileReferences(prompt),
	}
	if prompt == "" {
		features.State = CompletenessComplete
		return features
	}
	features.LineCount = strings.Count(prompt, "\n") + 1

	inWord := false
	latin, cyrillic, cjk, other := 0, 0, 0, 0
	for _, current := range prompt {
		letterOrNumber := unicode.IsLetter(current) || unicode.IsNumber(current)
		if letterOrNumber && !inWord {
			features.WordCount++
		}
		if letterOrNumber {
			inWord = true
		} else if !(unicode.IsMark(current) && inWord) {
			inWord = false
		}
		switch {
		case unicode.In(current, unicode.Latin):
			latin++
		case unicode.In(current, unicode.Cyrillic):
			cyrillic++
		case unicode.In(current, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			cjk++
		case unicode.IsLetter(current):
			other++
		}
	}
	features.CoarseScript = dominantScript(latin, cyrillic, cjk, other)
	return features
}

func dominantScript(latin, cyrillic, cjk, other int) string {
	values := []struct {
		name  string
		count int
	}{{"latin", latin}, {"cyrillic", cyrillic}, {"cjk", cjk}, {"other", other}}
	maximum := 0
	winner := "unknown"
	tied := false
	for _, item := range values {
		if item.count > maximum {
			maximum = item.count
			winner = item.name
			tied = false
		} else if item.count > 0 && item.count == maximum {
			tied = true
		}
	}
	if maximum == 0 {
		return "unknown"
	}
	if tied {
		return "mixed"
	}
	return winner
}

func countFileReferences(value string) int {
	count := 0
	fields := strings.Fields(value)
	for _, field := range fields {
		trimmed := strings.Trim(field, "()[]{}<>,;:'\"")
		if strings.HasPrefix(trimmed, "./") || strings.HasPrefix(trimmed, "../") ||
			strings.HasPrefix(trimmed, "/") || strings.Contains(trimmed, "\\") {
			count++
		}
	}
	return count
}
