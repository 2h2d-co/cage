package cage

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

func readSecretInput(prompt string, forceStdin bool) ([]byte, error) {
	stdinIsTerminal := term.IsTerminal(int(os.Stdin.Fd()))
	if forceStdin || !stdinIsTerminal {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			zeroBytes(data)
			return nil, fmt.Errorf("read token from stdin: %w", err)
		}
		return trimTrailingNewlines(data), nil
	}

	if _, err := fmt.Fprint(os.Stderr, prompt); err != nil {
		return nil, err
	}
	data, err := term.ReadPassword(int(os.Stdin.Fd()))
	if _, printErr := fmt.Fprintln(os.Stderr); printErr != nil {
		err = errors.Join(err, printErr)
	}
	if err != nil {
		zeroBytes(data)
		return nil, fmt.Errorf("read secret input: %w", err)
	}
	return trimTrailingNewlines(data), nil
}

func trimTrailingNewlines(data []byte) []byte {
	end := len(data)
	for end > 0 && (data[end-1] == '\n' || data[end-1] == '\r') {
		end--
	}
	zeroBytes(data[end:])
	return data[:end]
}

func confirm(prompt string, assumeYes bool) (confirmed bool, err error) {
	if assumeYes {
		return true, nil
	}

	input := os.Stdin
	closeInput := func() error { return nil }
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		file, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
		if err != nil {
			return false, errors.New("confirmation requires a terminal; pass --yes to confirm non-interactively")
		}
		input = file
		closeInput = file.Close
	}
	defer func() {
		err = errors.Join(err, closeInput())
	}()

	if _, err := fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt); err != nil {
		return false, err
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
