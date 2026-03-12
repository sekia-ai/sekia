package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage secret encryption",
	}

	cmd.AddCommand(newSecretsKeygenCmd())
	cmd.AddCommand(newSecretsEncryptCmd())
	cmd.AddCommand(newSecretsDecryptCmd())
	cmd.AddCommand(newSecretsKMSEncryptCmd())
	cmd.AddCommand(newSecretsKMSDecryptCmd())
	cmd.AddCommand(newSecretsASMGetCmd())

	return cmd
}

func newSecretsKeygenCmd() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "keygen",
		Short: "Generate a new age keypair for config encryption",
		Long: `Generates a new X25519 age keypair and writes the identity (private key) to a
file. The public key (recipient) is printed to stdout for use with
'sekiactl secrets encrypt --recipient'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			identity, err := secrets.GenerateKeyPair()
			if err != nil {
				return fmt.Errorf("generate keypair: %w", err)
			}

			if output == "" {
				homeDir, _ := os.UserHomeDir()
				output = filepath.Join(homeDir, ".config", "sekia", secrets.DefaultKeyFilename)
			}

			if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
				return fmt.Errorf("create directory: %w", err)
			}

			if _, err := os.Stat(output); err == nil {
				return fmt.Errorf("key file already exists: %s (remove it first to regenerate)", output)
			}

			content := fmt.Sprintf("# created: %s\n# public key: %s\n%s\n",
				time.Now().Format(time.RFC3339),
				identity.Recipient().String(),
				identity.String(),
			)
			if err := os.WriteFile(output, []byte(content), 0600); err != nil {
				return fmt.Errorf("write key file: %w", err)
			}

			fmt.Printf("Key file written to: %s\n", output)
			fmt.Printf("Public key: %s\n", identity.Recipient().String())
			return nil
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "output path (default: ~/.config/sekia/age.key)")
	return cmd
}

func newSecretsEncryptCmd() *cobra.Command {
	var recipientKey string

	cmd := &cobra.Command{
		Use:   "encrypt <value>",
		Short: "Encrypt a value for use in config files",
		Long: `Encrypts a plaintext value and outputs the ENC[...] string to paste into a
TOML config file. If --recipient is not provided, the public key is read from
the default key file (~/.config/sekia/age.key).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var recipient age.Recipient

			if recipientKey != "" {
				r, err := age.ParseX25519Recipient(recipientKey)
				if err != nil {
					return fmt.Errorf("parse recipient: %w", err)
				}
				recipient = r
			} else {
				// Read public key from key file via identity resolution.
				v := viper.New()
				ids, err := secrets.ResolveIdentity(v)
				if err != nil {
					return fmt.Errorf("resolve identity: %w", err)
				}
				if ids == nil {
					return fmt.Errorf("no age key found; run 'sekiactl secrets keygen' first or use --recipient")
				}
				x25519, ok := ids[0].(*age.X25519Identity)
				if !ok {
					return fmt.Errorf("default key is not an X25519 identity; use --recipient to specify a public key")
				}
				recipient = x25519.Recipient()
			}

			encrypted, err := secrets.Encrypt(args[0], recipient)
			if err != nil {
				return fmt.Errorf("encrypt: %w", err)
			}

			fmt.Println(encrypted)
			return nil
		},
	}

	cmd.Flags().StringVar(&recipientKey, "recipient", "", "age public key (default: read from key file)")
	return cmd
}

func newSecretsDecryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "decrypt <encrypted-value>",
		Short: "Decrypt an ENC[...] value (for debugging)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			v := viper.New()
			ids, err := secrets.ResolveIdentity(v)
			if err != nil {
				return fmt.Errorf("resolve identity: %w", err)
			}
			if ids == nil {
				return fmt.Errorf("no age identity found; set SEKIA_AGE_KEY, SEKIA_AGE_KEY_FILE, or provide a key file at ~/.config/sekia/age.key")
			}

			plaintext, err := secrets.Decrypt(args[0], ids...)
			if err != nil {
				return err
			}

			fmt.Println(plaintext)
			return nil
		},
	}
}

func newSecretsKMSEncryptCmd() *cobra.Command {
	var keyID string

	cmd := &cobra.Command{
		Use:   "kms-encrypt <value>",
		Short: "Encrypt a value using AWS KMS",
		Long: `Encrypts a plaintext value using an AWS KMS key and outputs a KMS[...] string
to paste into a TOML config file. The KMS key ID can be an ARN, alias, or key ID.

AWS credentials are resolved via the standard SDK default chain (environment,
profile, instance role, etc.).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyID == "" {
				keyID = os.Getenv(secrets.EnvKMSKeyID)
			}
			if keyID == "" {
				v := viper.New()
				keyID = v.GetString("secrets.kms_key_id")
			}
			if keyID == "" {
				return fmt.Errorf("KMS key ID is required; use --key-id, set %s, or configure secrets.kms_key_id", secrets.EnvKMSKeyID)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			v := viper.New()
			awsCfg, err := secrets.LoadAWSConfig(ctx, v)
			if err != nil {
				return err
			}

			client := kms.NewFromConfig(awsCfg)
			encrypted, err := secrets.KMSEncrypt(ctx, client, keyID, args[0])
			if err != nil {
				return err
			}

			fmt.Println(encrypted)
			return nil
		},
	}

	cmd.Flags().StringVar(&keyID, "key-id", "", "KMS key ID, ARN, or alias (default: $SEKIA_KMS_KEY_ID or secrets.kms_key_id)")
	return cmd
}

func newSecretsKMSDecryptCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kms-decrypt <KMS[...]-value>",
		Short: "Decrypt a KMS[...] value (for debugging)",
		Long: `Decrypts a KMS[...] value using the AWS KMS API. The key ID is embedded in the
ciphertext, so no key ID parameter is needed. AWS credentials are resolved via
the standard SDK default chain.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			v := viper.New()
			awsCfg, err := secrets.LoadAWSConfig(ctx, v)
			if err != nil {
				return err
			}

			client := kms.NewFromConfig(awsCfg)
			plaintext, err := secrets.KMSDecrypt(ctx, client, args[0])
			if err != nil {
				return err
			}

			fmt.Println(plaintext)
			return nil
		},
	}
}

func newSecretsASMGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "asm-get <secret-name-or-arn>",
		Short: "Fetch a plaintext secret from AWS Secrets Manager (for debugging)",
		Long: `Retrieves a plaintext secret from AWS Secrets Manager and prints it to stdout.
Only plaintext secrets are supported; binary secrets return an error.

The argument is the secret name or ARN (without the ASM[...] wrapper).
AWS credentials are resolved via the standard SDK default chain.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			v := viper.New()
			awsCfg, err := secrets.LoadAWSConfig(ctx, v)
			if err != nil {
				return err
			}

			client := secretsmanager.NewFromConfig(awsCfg)
			// Wrap in ASM[...] for the API.
			ref := "ASM[" + args[0] + "]"
			plaintext, err := secrets.ASMFetch(ctx, client, ref)
			if err != nil {
				return err
			}

			fmt.Println(plaintext)
			return nil
		},
	}
}
