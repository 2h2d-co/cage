package cage

import (
	"fmt"
	"os"
	"strings"
	"unicode"

	"golang.org/x/term"
)

func notifyActionNeeded(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	if term.IsTerminal(int(os.Stderr.Fd())) {
		_, _ = fmt.Fprint(os.Stderr, "\a")
		_, _ = fmt.Fprintf(os.Stderr, "\x1b]9;%s\x1b\\", terminalNotificationText(message))
	}
	_, _ = fmt.Fprintf(os.Stderr, "cage: action needed: %s\n", message)
}

func terminalNotificationText(message string) string {
	var builder strings.Builder
	for _, value := range message {
		switch {
		case value == '\a' || value == '\x1b':
			continue
		case value == '\n' || value == '\r' || value == '\t':
			builder.WriteByte(' ')
		case unicode.IsControl(value):
			continue
		default:
			builder.WriteRune(value)
		}
	}
	return strings.Join(strings.Fields(builder.String()), " ")
}
