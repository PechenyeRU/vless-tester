package naming

import "strings"

// Emoji converts an ISO-3166 alpha-2 country code into its regional-indicator
// flag emoji (e.g. "FR" -> 🇫🇷). It returns "" for anything that is not two
// ASCII letters so callers can fall back to a neutral label.
func Emoji(country string) string {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return ""
	}
	var b strings.Builder
	for i := range 2 {
		c := country[i]
		if c < 'A' || c > 'Z' {
			return ""
		}
		// Map A-Z to the regional indicator symbols U+1F1E6..U+1F1FF.
		b.WriteRune(rune(0x1F1E6 + int(c-'A')))
	}
	return b.String()
}
