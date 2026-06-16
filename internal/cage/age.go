package cage

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"filippo.io/age"
	"filippo.io/age/plugin"
	"filippo.io/age/tag"
	"golang.org/x/term"
)

var ageRecipientPattern = regexp.MustCompile(`age1[A-Za-z0-9+._-]+`)

func readAgeIdentities(path string) ([]age.Identity, error) {
	if err := ensurePrivateFile(path, "identity file"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read identity file %s: %w", path, err)
	}
	defer zeroBytes(data)

	var identities []age.Identity
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 || bytes.HasPrefix(line, []byte("#")) {
			continue
		}

		// filippo.io/age identity parsers and the plugin client API accept strings.
		// Cage zeroes the source file buffer, but those parser-bound string copies
		// cannot be explicitly cleared by Go code.
		identity, err := parseAgeIdentity(string(line))
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
		return nil, errors.New("unsupported age identity type")
	}
}

func recipientFromIdentityFilePublicComment(identityFile string) (age.Recipient, error) {
	recipients, err := readIdentityFilePublicRecipients(identityFile)
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("identity file %s has no public recipient comment", identityFile)
	}
	if len(recipients) != 1 {
		return nil, fmt.Errorf("identity file %s contains %d public recipient comments; provider encryption expects exactly one", identityFile, len(recipients))
	}
	recipient, err := parseAgeRecipient(recipients[0])
	if err != nil {
		return nil, fmt.Errorf("parse public recipient in identity file %s: %w", identityFile, err)
	}
	return recipient, nil
}

func readIdentityFilePublicRecipients(path string) ([]string, error) {
	if err := ensurePrivateFile(path, "identity file"); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read identity file %s: %w", path, err)
	}
	defer zeroBytes(data)
	return publicRecipientsInIdentityData(data), nil
}

func publicRecipientsInIdentityData(data []byte) []string {
	seen := map[string]bool{}
	recipients := []string{}
	remaining := data
	for len(remaining) > 0 {
		line := remaining
		if before, after, found := bytes.Cut(remaining, []byte("\n")); found {
			line = before
			remaining = after
		} else {
			remaining = nil
		}

		comment, ok := bytes.CutPrefix(bytes.TrimSpace(line), []byte("#"))
		if !ok {
			continue
		}
		for _, match := range ageRecipientPattern.FindAll(comment, -1) {
			recipient := strings.TrimRight(string(match), ".,;:)]}>\"'")
			if recipient == "" || seen[recipient] {
				continue
			}
			if _, err := parseAgeRecipient(recipient); err != nil {
				continue
			}
			seen[recipient] = true
			recipients = append(recipients, recipient)
		}
	}
	return recipients
}

func parseAgeRecipient(line string) (age.Recipient, error) {
	if recipient, err := age.ParseX25519Recipient(line); err == nil {
		return recipient, nil
	}
	if recipient, err := age.ParseHybridRecipient(line); err == nil {
		return recipient, nil
	}
	if recipient, err := tag.ParseRecipient(line); err == nil {
		return recipient, nil
	}
	recipient, err := plugin.NewRecipient(line, newPluginUI())
	if err != nil {
		return nil, errors.New("unsupported age recipient type")
	}
	return recipient, nil
}

func encryptWithSingleIdentity(plaintext []byte, identityFile string) ([]byte, error) {
	recipient, err := recipientFromIdentityFilePublicComment(identityFile)
	if err != nil {
		return nil, err
	}

	var out bytes.Buffer
	encrypt := func() error {
		writer, err := age.Encrypt(&out, recipient)
		if err != nil {
			return withAgeInstallHint(err)
		}
		if _, err := writer.Write(plaintext); err != nil {
			if closeErr := writer.Close(); closeErr != nil {
				return errors.Join(err, withAgeInstallHint(closeErr))
			}
			return err
		}
		if err := writer.Close(); err != nil {
			return withAgeInstallHint(err)
		}
		return nil
	}
	if recipientUsesPlugin(recipient) {
		if err := withPluginChildEnvironment(encrypt); err != nil {
			return nil, err
		}
	} else if err := encrypt(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func decryptWithIdentityFile(ciphertext []byte, identityFile string) ([]byte, error) {
	identities, err := readAgeIdentities(identityFile)
	if err != nil {
		return nil, err
	}
	var plaintext []byte
	decrypt := func() error {
		reader, err := age.Decrypt(bytes.NewReader(ciphertext), identities...)
		if err != nil {
			return withAgeInstallHint(err)
		}
		plaintext, err = io.ReadAll(reader)
		if err != nil {
			return err
		}
		return nil
	}
	if identitiesUsePlugin(identities) {
		if err := withPluginChildEnvironment(decrypt); err != nil {
			return nil, err
		}
	} else if err := decrypt(); err != nil {
		return nil, err
	}
	return plaintext, nil
}

func identitiesUsePlugin(identities []age.Identity) bool {
	for _, identity := range identities {
		if identityUsesPlugin(identity) {
			return true
		}
	}
	return false
}

func identityUsesPlugin(identity age.Identity) bool {
	_, ok := identity.(*plugin.Identity)
	return ok
}

func recipientUsesPlugin(recipient age.Recipient) bool {
	_, ok := recipient.(*plugin.Recipient)
	return ok
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
		// The age plugin UI callback interface returns strings. Cage clears the
		// terminal byte buffer, but cannot explicitly clear the returned string.
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
