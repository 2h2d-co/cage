package cage

import "regexp"

var redactors = []*regexp.Regexp{
	regexp.MustCompile(`ops_[A-Za-z0-9_+=/.-]+`),
	regexp.MustCompile(`AGE-SECRET-KEY-PQ-[A-Z0-9]+`),
	regexp.MustCompile(`AGE-SECRET-KEY-[A-Z0-9]+`),
	regexp.MustCompile(`AGE-PLUGIN-[A-Z0-9-]+`),
	regexp.MustCompile(`(?i)(token|secret|password|passwd|api[_-]?key|authorization)(\s*[:=]\s*)[^\r\n]*`),
}

// Redact replaces common secret-looking substrings with a placeholder.
func Redact(s string) string {
	out := s
	for _, re := range redactors {
		out = re.ReplaceAllStringFunc(out, func(match string) string {
			parts := re.FindStringSubmatch(match)
			if len(parts) == 3 {
				return parts[1] + parts[2] + "<redacted>"
			}
			return "<redacted>"
		})
	}
	return out
}
