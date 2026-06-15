package cage

import "testing"

func TestMacOSNotificationScriptEscapesMessage(t *testing.T) {
	got := macOSNotificationScript(`touch "YubiKey" \ now`)
	want := `display notification "touch \"YubiKey\" \\ now" with title "cage" sound name "Ping"`
	if got != want {
		t.Fatalf("macOSNotificationScript() = %q, want %q", got, want)
	}
}

func TestTerminalNotificationTextRemovesControlSequences(t *testing.T) {
	got := terminalNotificationText("touch\nYubiKey\x1b]9;bad\a now")
	want := "touch YubiKey]9;bad now"
	if got != want {
		t.Fatalf("terminalNotificationText() = %q, want %q", got, want)
	}
}
