package cage

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/plugin"
	"golang.org/x/term"
)

func readAgeIdentities(path string) ([]age.Identity, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read identity file %s: %w", path, err)
	}

	var identities []age.Identity
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		identity, err := parseAgeIdentity(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNumber, err)
		}
		identities = append(identities, identity)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read identity file %s: %w", path, err)
	}
	if len(identities) == 0 {
		return nil, fmt.Errorf("identity file %s has no identities", path)
	}
	return identities, nil
}

func parseAgeIdentity(line string) (age.Identity, error) {
	switch {
	case strings.HasPrefix(line, "AGE-SECRET-KEY-PQ-1"):
		return age.ParseHybridIdentity(line)
	case strings.HasPrefix(line, "AGE-SECRET-KEY-1"):
		return age.ParseX25519Identity(line)
	case strings.HasPrefix(line, "AGE-PLUGIN-"):
		identity, err := plugin.NewIdentity(line, newPluginUI())
		if err != nil {
			return nil, err
		}
		return identity, nil
	default:
		return nil, fmt.Errorf("unsupported age identity type")
	}
}

func encryptWithSingleIdentity(plaintext []byte, identityFile string) ([]byte, error) {
	identities, err := readAgeIdentities(identityFile)
	if err != nil {
		return nil, err
	}
	if len(identities) != 1 {
		return nil, fmt.Errorf("identity file %s contains %d identities; provider encryption expects exactly one", identityFile, len(identities))
	}

	recipient, err := recipientForIdentity(identities[0])
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	writer, err := age.Encrypt(&out, recipient)
	if err != nil {
		return nil, withAgeInstallHint(err)
	}
	if _, err := writer.Write(plaintext); err != nil {
		if closeErr := writer.Close(); closeErr != nil {
			return nil, errors.Join(err, withAgeInstallHint(closeErr))
		}
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, withAgeInstallHint(err)
	}
	return out.Bytes(), nil
}

func decryptWithIdentityFile(ciphertext []byte, identityFile string) ([]byte, error) {
	identities, err := readAgeIdentities(identityFile)
	if err != nil {
		return nil, err
	}
	reader, err := age.Decrypt(bytes.NewReader(ciphertext), identities...)
	if err != nil {
		return nil, withAgeInstallHint(err)
	}
	plaintext, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	return plaintext, nil
}

func recipientForIdentity(identity age.Identity) (age.Recipient, error) {
	switch id := identity.(type) {
	case *age.X25519Identity:
		return id.Recipient(), nil
	case *age.HybridIdentity:
		return id.Recipient(), nil
	case *plugin.Identity:
		return id.Recipient(), nil
	default:
		return nil, fmt.Errorf("identity type %T cannot be used as an encryption recipient", identity)
	}
}

func withAgeInstallHint(err error) error {
	var notFound *plugin.NotFoundError
	if errors.As(err, &notFound) {
		return fmt.Errorf("%w. %s", err, pluginInstallHint(notFound.Name))
	}
	return err
}

func pluginInstallHint(name string) string {
	switch name {
	case "yubikey":
		return "Install hint: brew install age-plugin-yubikey"
	case "se":
		return "Install hint: brew install age-plugin-se"
	case "tag":
		return "Install hint: install age >= 1.3 or the age-plugin-tag-compatible plugin required by the recipient"
	default:
		return fmt.Sprintf("Install hint: install age-plugin-%s and make it available on PATH", name)
	}
}

func newPluginUI() *plugin.ClientUI {
	return &plugin.ClientUI{
		DisplayMessage: func(name, message string) error {
			_, err := fmt.Fprintf(os.Stderr, "age-plugin-%s: %s\n", name, message)
			return err
		},
		RequestValue: func(name, prompt string, secret bool) (string, error) {
			return requestPluginValue(name, prompt, secret)
		},
		Confirm: func(name, prompt, yes, no string) (bool, error) {
			label := prompt
			if yes != "" && no != "" {
				label = fmt.Sprintf("%s (%s/%s)", prompt, yes, no)
			}
			return confirm("age-plugin-"+name+": "+label, false)
		},
		WaitTimer: func(name string) {
			notifyActionNeeded(pluginWaitMessage(name))
		},
	}
}

func pluginWaitMessage(name string) string {
	if name == "yubikey" {
		return "touch the YubiKey when it blinks"
	}
	return fmt.Sprintf("age-plugin-%s is waiting for hardware or user confirmation", name)
}

func pluginSecretInputMessage(name string) string {
	if name == "yubikey" {
		return "enter the YubiKey PIN, then touch the YubiKey when it blinks"
	}
	return fmt.Sprintf("age-plugin-%s needs secure input", name)
}

func requestPluginValue(name, prompt string, secret bool) (value string, err error) {
	input := os.Stdin
	closeInput := func() error { return nil }
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		file, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err != nil {
			return "", fmt.Errorf("age-plugin-%s requested input but stdin is not a terminal", name)
		}
		input = file
		closeInput = file.Close
	}
	defer func() {
		err = errors.Join(err, closeInput())
	}()

	if secret {
		prompt = strings.TrimSpace(prompt)
		if prompt == "" {
			prompt = "secure input requested"
		}
		notifyActionNeeded(pluginSecretInputMessage(name))
		if _, err := fmt.Fprintf(os.Stderr, "age-plugin-%s: %s\n", name, prompt); err != nil {
			return "", err
		}
		data, err := term.ReadPassword(int(input.Fd()))
		if _, printErr := fmt.Fprintln(os.Stderr); printErr != nil {
			err = errors.Join(err, printErr)
		}
		if err != nil {
			zeroBytes(data)
			return "", err
		}
		value := string(data)
		zeroBytes(data)
		return value, nil
	}

	if _, err := fmt.Fprintf(os.Stderr, "age-plugin-%s: %s", name, prompt); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(input).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
