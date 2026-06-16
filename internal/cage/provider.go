package cage

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func (a *App) newProviderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "provider",
		Short: "Manage encrypted provider identities",
		Long:  "Manage provider identities such as age-encrypted 1Password service account tokens.",
	}
	cmd.AddCommand(a.new1PasswordProviderCommand())
	return cmd
}

func (a *App) new1PasswordProviderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "1p",
		Aliases: []string{"1password", "op"},
		Short:   "Manage 1Password providers",
	}
	cmd.AddCommand(a.new1PasswordProviderCreateCommand())
	return cmd
}

func (a *App) new1PasswordProviderCreateCommand() *cobra.Command {
	var identityName string
	var fromStdin bool
	var yes bool

	cmd := &cobra.Command{
		Use:   "create NAME --identity IDENTITY",
		Short: "Create an encrypted 1Password service account provider",
		Long:  "Read a 1Password service account token securely or from stdin, encrypt it to one configured age identity, write NAME.1p.age with mode 0600, and update [providers] in config.toml.",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if err := requireMacOS(); err != nil {
				return err
			}
			providerName := args[0]
			if err := ValidateCreatedName("provider", providerName); err != nil {
				return err
			}
			if identityName == "" {
				return errors.New("--identity is required")
			}

			cfg, err := a.loadConfig()
			if err != nil {
				return err
			}
			identity, ok := cfg.Identities[identityName]
			if !ok {
				return fmt.Errorf("unknown identity %q", identityName)
			}

			fileName := providerName + ".1p.age"
			path := cfg.ResolveFile(fileName)
			if err := a.confirmProviderOverwrite(cfg, providerName, path, yes); err != nil {
				return err
			}

			token, err := readSecretInput("1Password service account token: ", fromStdin)
			if err != nil {
				return err
			}
			defer zeroBytes(token)
			if len(token) == 0 {
				return errors.New("1Password service account token is empty")
			}

			ciphertext, err := encryptWithSingleIdentity(token, cfg.ResolveFile(identity.File))
			if err != nil {
				return err
			}
			if err := writeSecretFile(path, ciphertext); err != nil {
				return err
			}

			cfg.Providers[providerName] = ProviderConfig{Type: ProviderType1Password, Identity: identityName, File: fileName}
			if err := cfg.Write(); err != nil {
				return err
			}
			_, err = fmt.Fprintf(a.out, "created 1password provider %s at %s\n", providerName, path)
			return err
		},
	}
	cmd.Flags().StringVar(&identityName, "identity", "", "configured identity used to encrypt the provider token")
	cmd.Flags().BoolVar(&fromStdin, "stdin", false, "read the plaintext token from stdin")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "answer yes to overwrite confirmations")
	return cmd
}

func (a *App) confirmProviderOverwrite(cfg *Config, name string, path string, yes bool) error {
	_, configured := cfg.Providers[name]
	exists, err := fileExists(path)
	if err != nil {
		return err
	}
	if !configured && !exists {
		return nil
	}
	ok, err := confirm(fmt.Sprintf("Overwrite provider %q and %s?", name, path), yes)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("create cancelled")
	}
	return nil
}

func zeroBytes(data []byte) {
	clear(data)
	runtime.KeepAlive(data)
}
