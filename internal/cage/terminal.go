package cage

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unicode"
)

func notifyActionNeeded(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}

	postMacOSNotification(terminalNotificationText(message))
	_, _ = fmt.Fprintf(os.Stderr, "cage: action needed: %s\n", message)
}

func postMacOSNotification(text string) {
	if runtime.GOOS != "darwin" || text == "" {
		return
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer func() { _ = devNull.Close() }()

	process, err := os.StartProcess("/usr/bin/osascript", []string{"osascript", "-e", macOSNotificationScript(text)}, &os.ProcAttr{
		Files: []*os.File{devNull, devNull, devNull},
		Env:   macOSNotificationEnvironment(),
	})
	if err != nil {
		return
	}
	_, _ = process.Wait()
}

func macOSNotificationScript(text string) string {
	return "display notification " + appleScriptStringLiteral(text) + " with title " + appleScriptStringLiteral("cage") + " sound name " + appleScriptStringLiteral("Ping")
}

func appleScriptStringLiteral(value string) string {
	var builder strings.Builder
	builder.WriteByte('"')
	for _, char := range value {
		if char == '\\' || char == '"' {
			builder.WriteByte('\\')
		}
		builder.WriteRune(char)
	}
	builder.WriteByte('"')
	return builder.String()
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
