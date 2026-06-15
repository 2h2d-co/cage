package cage

import "testing"

func TestTerminalNotificationTextRemovesControlSequences(t *testing.T) {
	got := terminalNotificationText("touch\nYubiKey\x1b]9;bad\a now")
	want := "touch YubiKey]9;bad now"
	if got != want {
		t.Fatalf("terminalNotificationText() = %q, want %q", got, want)
	}
}
