package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/sekia-ai/sekia/internal/secrets"
)

func newSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Encrypt config values with AWS KMS",
	}

	cmd.AddCommand(newSecretsKMSEncryptCmd())
	cmd.AddCommand(newSecretsKMSDecryptCmd())

	return cmd
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
