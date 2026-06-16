package cage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"filippo.io/age"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (a *App) newIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "identity",
		Short: "Manage age identities",
		Long:  "Manage cage-tracked age identities backed by native age keys, age-plugin-yubikey, and age-plugin-se.",
	}
	cmd.AddCommand(a.newBasicIdentityCommand())
	cmd.AddCommand(a.newYubiKeyIdentityCommand())
	cmd.AddCommand(a.newSecureEnclaveIdentityCommand())
	return cmd
}

func (a *App) newBasicIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "basic",
		Short: "Manage native age identities",
		Long:  "Manage cage-tracked native age identities that do not require age plugins.",
	}
	cmd.AddCommand(a.newBasicCreateCommand())
	cmd.AddCommand(a.newBasicListCommand())
	cmd.AddCommand(a.newIdentityDeleteCommand(IdentityTypeBasic, "basic"))
	return cmd
}

func (a *App) newBasicCreateCommand() *cobra.Command {
	var pq bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a basic age identity",
		Long:  "Create a native age identity, write NAME.identity with mode 0600, and update [identities] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			if err := ValidateCreatedName("identity", name); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			fileName := name + ".identity"
			path := cfg.ResolveFile(fileName)
			if err := a.confirmIdentityOverwrite(cfg, name, path, yes); err != nil {
				return err
			}

			var recipient string
			var secret string
			if pq {
				identity, err := age.GenerateHybridIdentity()
				if err != nil {
					return err
				}
				recipient = identity.Recipient().String()
				secret = identity.String()
			} else {
				identity, err := age.GenerateX25519Identity()
				if err != nil {
					return err
				}
				recipient = identity.Recipient().String()
				secret = identity.String()
			}

			// The age API exposes generated secret identities as strings. Cage cannot
			// explicitly zero those string copies, but it zeroes the owned byte buffer
			// as soon as the identity file has been written.
			data := make([]byte, 0, len("# public key: ")+len(recipient)+1+len(secret)+1)
			data = append(data, "# public key: "...)
			data = append(data, recipient...)
			data = append(data, '\n')
			data = append(data, secret...)
			data = append(data, '\n')
			defer zeroBytes(data)
			if err := writeSecretFile(path, data); err != nil {
				return err
			}
			zeroBytes(data)
			cfg.Identities[name] = IdentityConfig{Type: IdentityTypeBasic, File: fileName}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created basic identity %s at %s\n", name, path)
			return err
		},
	}
	cmd.Flags().BoolVar(&pq, "pq", false, "generate a post-quantum native age identity")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) newBasicListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List basic age identities",
		Long:  "List cage-configured native age identities.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			return a.listConfiguredIdentities(cfg, IdentityTypeBasic, "basic")
		},
	}
}

func (a *App) newYubiKeyIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "yubikey",
		Aliases: []string{"yk"},
		Short:   "Manage age-plugin-yubikey identities",
	}
	cmd.AddCommand(a.newYubiKeyCreateCommand())
	cmd.AddCommand(a.newYubiKeyListCommand())
	cmd.AddCommand(a.newIdentityDeleteCommand(IdentityTypeYubiKey, "yubikey"))
	return cmd
}

func (a *App) newSecureEnclaveIdentityCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "se",
		Aliases: []string{"secure-enclave"},
		Short:   "Manage age-plugin-se identities",
	}
	cmd.AddCommand(a.newSecureEnclaveCreateCommand())
	cmd.AddCommand(a.newSecureEnclaveListCommand())
	cmd.AddCommand(a.newIdentityDeleteCommand(IdentityTypeSecureEnclave, "secure-enclave"))
	return cmd
}

func (a *App) newYubiKeyCreateCommand() *cobra.Command {
	var serial string
	var slot int
	var pinPolicy string
	var touchPolicy string
	var forceSlot bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a YubiKey age identity",
		Long:  "Create a YubiKey age identity with age-plugin-yubikey, write NAME.identity with mode 0600, and update [identities] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			if err := ValidateCreatedName("identity", name); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			fileName := name + ".identity"
			path := cfg.ResolveFile(fileName)
			if err := a.confirmIdentityOverwrite(cfg, name, path, yes); err != nil {
				return err
			}
			if err := requireTool("age-plugin-yubikey"); err != nil {
				return err
			}

			pluginArgs := []string{"--generate", "--name", name}
			if serial != "" {
				pluginArgs = append(pluginArgs, "--serial", serial)
			}
			if cmd.Flags().Changed("slot") {
				pluginArgs = append(pluginArgs, "--slot", fmt.Sprint(slot))
			}
			if pinPolicy != "" {
				pluginArgs = append(pluginArgs, "--pin-policy", pinPolicy)
			}
			if touchPolicy != "" {
				pluginArgs = append(pluginArgs, "--touch-policy", touchPolicy)
			}
			if forceSlot {
				pluginArgs = append(pluginArgs, "--force")
			}

			identity, err := runPluginCapture("age-plugin-yubikey", pluginArgs)
			if err != nil {
				return err
			}
			defer zeroBytes(identity)
			if err := writeSecretFile(path, identity); err != nil {
				return err
			}
			zeroBytes(identity)
			cfg.Identities[name] = IdentityConfig{Type: IdentityTypeYubiKey, File: fileName}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created yubikey identity %s at %s\n", name, path)
			return err
		},
	}
	cmd.Flags().StringVar(&serial, "serial", "", "YubiKey serial number")
	cmd.Flags().IntVar(&slot, "slot", 0, "YubiKey retired PIV slot number")
	cmd.Flags().StringVar(&pinPolicy, "pin-policy", "", "YubiKey PIN policy: always, once, or never")
	cmd.Flags().StringVar(&touchPolicy, "touch-policy", "", "YubiKey touch policy: always, cached, or never")
	cmd.Flags().BoolVar(&forceSlot, "force-slot", false, "ask age-plugin-yubikey to overwrite a filled YubiKey slot")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) newSecureEnclaveCreateCommand() *cobra.Command {
	var accessControl string
	var recipientType string
	var pq bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a Secure Enclave age identity",
		Long:  "Create a Secure Enclave age identity with age-plugin-se, write NAME.identity with mode 0600, and update [identities] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			if err := ValidateCreatedName("identity", name); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			fileName := name + ".identity"
			path := cfg.ResolveFile(fileName)
			if err := a.confirmIdentityOverwrite(cfg, name, path, yes); err != nil {
				return err
			}
			if err := requireTool("age-plugin-se"); err != nil {
				return err
			}

			pluginArgs := []string{"keygen", "--access-control", accessControl, "--recipient-type", recipientType}
			if pq {
				pluginArgs = append(pluginArgs, "--pq")
			}
			identity, err := runPluginCapture("age-plugin-se", pluginArgs)
			if err != nil {
				return err
			}
			defer zeroBytes(identity)
			if err := writeSecretFile(path, identity); err != nil {
				return err
			}
			zeroBytes(identity)
			cfg.Identities[name] = IdentityConfig{Type: IdentityTypeSecureEnclave, File: fileName}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created secure-enclave identity %s at %s\n", name, path)
			return err
		},
	}
	cmd.Flags().StringVar(&accessControl, "access-control", "any-biometry", "Secure Enclave access control: none, passcode, any-biometry, any-biometry-and-passcode, any-biometry-or-passcode, current-biometry, current-biometry-and-passcode")
	cmd.Flags().StringVar(&recipientType, "recipient-type", "se", "recipient type generated by age-plugin-se: se or tag")
	cmd.Flags().BoolVar(&pq, "pq", false, "generate post-quantum Secure Enclave identity")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) newYubiKeyListCommand() *cobra.Command {
	var connected bool
	var all bool
	var serial string
	var slot int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List YubiKey identities",
		Long:  "List cage-configured YubiKey identities. With --connected, also call age-plugin-yubikey to list connected YubiKey recipients.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			if err := a.listConfiguredIdentities(cfg, IdentityTypeYubiKey, "yubikey"); err != nil {
				return err
			}
			if !connected {
				return nil
			}
			if err := requireTool("age-plugin-yubikey"); err != nil {
				return err
			}
			pluginArgs := []string{"--list"}
			if all {
				pluginArgs = []string{"--list-all"}
			}
			if serial != "" {
				pluginArgs = append(pluginArgs, "--serial", serial)
			}
			if cmd.Flags().Changed("slot") {
				pluginArgs = append(pluginArgs, "--slot", fmt.Sprint(slot))
			}
			if _, err := fmt.Fprintln(a.out, "\nConnected YubiKeys:"); err != nil {
				return err
			}
			return runPluginPassthrough("age-plugin-yubikey", pluginArgs, a.out, a.errOut)
		},
	}
	cmd.Flags().BoolVar(&connected, "connected", false, "also list recipients from connected YubiKeys")
	cmd.Flags().BoolVar(&all, "all", false, "with --connected, list all compatible YubiKey slots")
	cmd.Flags().StringVar(&serial, "serial", "", "with --connected, filter by YubiKey serial")
	cmd.Flags().IntVar(&slot, "slot", 0, "with --connected, filter by YubiKey slot")
	return cmd
}

func (a *App) newSecureEnclaveListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Secure Enclave identities",
		Long:  "List cage-configured Secure Enclave identities.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			return a.listConfiguredIdentities(cfg, IdentityTypeSecureEnclave, "secure-enclave")
		},
	}
}

func (a *App) newIdentityDeleteCommand(identityType string, label string) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Delete a configured " + label + " identity",
		Long:  "Delete a cage identity entry and its local identity file after confirmation.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			name := args[0]
			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			identity, ok := cfg.Identities[name]
			if !ok {
				return fmt.Errorf("unknown identity %q", name)
			}
			if identity.Type != identityType {
				return fmt.Errorf("identity %q has type %q, not %q", name, identity.Type, identityType)
			}

			if err := cfg.validateConfigFilePath("identities."+name+".file", identity.File); err != nil {
				return err
			}
			path := cfg.ResolveFile(identity.File)
			if identityType == IdentityTypeYubiKey {
				if _, err := fmt.Fprintln(a.errOut, "note: age-plugin-yubikey does not expose key-material deletion; cage will remove only config and the local identity file"); err != nil {
					return err
				}
			}
			ok, err = confirm(fmt.Sprintf("Delete identity %q and %s?", name, path), yes)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("delete cancelled")
			}

			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove identity file %s: %w", path, err)
			}
			delete(cfg.Identities, name)
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "deleted %s identity %s\n", label, name)
			return err
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to delete confirmation")
	return cmd
}

func (a *App) confirmIdentityOverwrite(cfg *Config, name string, path string, yes bool) error {
	_, configured := cfg.Identities[name]
	exists, err := fileExists(path)
	if err != nil {
		return err
	}
	if !configured && !exists {
		return nil
	}

	ok, err := confirm(fmt.Sprintf("Overwrite identity %q and %s?", name, path), yes)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("create cancelled")
	}
	return nil
}

func (a *App) listConfiguredIdentities(cfg *Config, identityType string, label string) error {
	if _, err := fmt.Fprintf(a.out, "Configured %s identities:\n", label); err != nil {
		return err
	}
	count := 0
	for _, name := range sortedMapKeys(cfg.Identities) {
		identity := cfg.Identities[name]
		if identity.Type != identityType {
			continue
		}
		count++
		path := cfg.ResolveFile(identity.File)
		status := "missing"
		recipient := "-"
		if exists, err := fileExists(path); err != nil {
			status = "error: " + err.Error()
		} else if exists {
			if err := ensurePrivateFile(path, "identity file"); err != nil {
				return err
			}
			status = "present"
			foundRecipient, err := firstRecipientInIdentityFile(path)
			if err != nil {
				return err
			}
			if foundRecipient != "" {
				recipient = foundRecipient
			}
		}
		if _, err := fmt.Fprintf(a.out, "  %s\tfile=%s\tstatus=%s\trecipient=%s\n", name, identity.File, status, recipient); err != nil {
			return err
		}
	}
	if count == 0 {
		_, err := fmt.Fprintln(a.out, "  (none)")
		return err
	}
	return nil
}

func firstRecipientInIdentityFile(path string) (string, error) {
	recipients, err := readIdentityFilePublicRecipients(path)
	if err != nil {
		return "", err
	}
	if len(recipients) == 0 {
		return "", nil
	}
	return recipients[0], nil
}

func requireTool(binary string) error {
	if _, err := exec.LookPath(binary); err != nil {
		return fmt.Errorf("%s not found on PATH. %s", binary, toolInstallHint(binary))
	}
	return nil
}

func toolInstallHint(binary string) string {
	switch binary {
	case "age-plugin-yubikey":
		return "Install hint: brew install age-plugin-yubikey"
	case "age-plugin-se":
		return "Install hint: brew install age-plugin-se"
	case "age":
		return "Install hint: brew install age"
	default:
		return fmt.Sprintf("Install hint: install %s and make it available on PATH", binary)
	}
}

func runPluginCapture(binary string, args []string) ([]byte, error) {
	var stdout bytes.Buffer
	if err := runPluginProcessCaptureStdout(binary, args, &stdout); err != nil {
		return nil, fmt.Errorf("%s failed: %w", binary, err)
	}
	if stdout.Len() == 0 {
		return nil, fmt.Errorf("%s produced no identity output", binary)
	}
	return stdout.Bytes(), nil
}

func runPluginProcessCaptureStdout(binary string, args []string, stdout io.Writer) error {
	path, err := exec.LookPath(binary)
	if err != nil {
		return err
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutR.Close() }()

	stdin, closeStdin, err := pluginInputFile()
	if err != nil {
		return errors.Join(err, stdoutW.Close())
	}
	defer func() { _ = closeStdin() }()

	stderr, closeStderr, err := pluginErrorFile()
	if err != nil {
		return errors.Join(err, stdoutW.Close())
	}
	defer func() { _ = closeStderr() }()

	env, err := pluginChildEnvironment()
	if err != nil {
		return errors.Join(err, stdoutW.Close())
	}

	argv := append([]string{binary}, args...)
	process, err := os.StartProcess(path, argv, &os.ProcAttr{
		Files: []*os.File{stdin, stdoutW, stderr},
		Env:   env,
	})
	closeErr := stdoutW.Close()
	if err != nil {
		return errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}

	var wg sync.WaitGroup
	var stdoutErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, stdoutErr = io.Copy(stdout, stdoutR)
	}()

	state, waitErr := process.Wait()
	wg.Wait()
	if err := errors.Join(waitErr, stdoutErr); err != nil {
		return err
	}
	if !state.Success() {
		return fmt.Errorf("exited with %s", state.String())
	}
	return nil
}

func pluginInputFile() (*os.File, func() error, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return os.Stdin, func() error { return nil }, nil
	}
	file, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, errors.New("plugin command requires a terminal for interactive prompts")
	}
	return file, file.Close, nil
}

func pluginErrorFile() (*os.File, func() error, error) {
	if term.IsTerminal(int(os.Stderr.Fd())) {
		return os.Stderr, func() error { return nil }, nil
	}
	file, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, nil, errors.New("plugin command requires a terminal for interactive prompts")
	}
	return file, file.Close, nil
}

func runPluginPassthrough(binary string, args []string, stdout io.Writer, stderr io.Writer) error {
	if err := runPluginProcess(binary, args, stdout, stderr); err != nil {
		return fmt.Errorf("%s failed: %w", binary, err)
	}
	return nil
}

func runPluginProcess(binary string, args []string, stdout io.Writer, stderr io.Writer) error {
	path, err := exec.LookPath(binary)
	if err != nil {
		return err
	}

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutR.Close() }()

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return errors.Join(err, stdoutW.Close())
	}
	defer func() { _ = stderrR.Close() }()

	env, err := pluginChildEnvironment()
	if err != nil {
		return errors.Join(err, stdoutW.Close(), stderrW.Close())
	}

	argv := append([]string{binary}, args...)
	process, err := os.StartProcess(path, argv, &os.ProcAttr{
		Files: []*os.File{os.Stdin, stdoutW, stderrW},
		Env:   env,
	})
	closeErr := errors.Join(stdoutW.Close(), stderrW.Close())
	if err != nil {
		return errors.Join(err, closeErr)
	}
	if closeErr != nil {
		return closeErr
	}

	var wg sync.WaitGroup
	var stdoutErr error
	var stderrErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, stdoutErr = io.Copy(stdout, stdoutR)
	}()
	go func() {
		defer wg.Done()
		_, stderrErr = io.Copy(stderr, stderrR)
	}()

	state, waitErr := process.Wait()
	wg.Wait()
	if err := errors.Join(waitErr, stdoutErr, stderrErr); err != nil {
		return err
	}
	if !state.Success() {
		return fmt.Errorf("exited with %s", state.String())
	}
	return nil
}
