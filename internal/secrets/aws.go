// Package secrets — AWS KMS backend for config value encryption.
//
// Config values wrapped as KMS[<base64 ciphertext>] are decrypted at startup
// by every sekia binary via ResolveViperConfig.
package secrets

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/viper"
)

const (
	kmsPrefix = "KMS["
	kmsSuffix = "]"

	// EnvKMSKeyID is the env var for the default KMS key ID used for encryption.
	EnvKMSKeyID = "SEKIA_KMS_KEY_ID"
)

// KMSClient is the subset of the AWS KMS API used for encrypt/decrypt.
type KMSClient interface {
	Encrypt(ctx context.Context, input *kms.EncryptInput, optFns ...func(*kms.Options)) (*kms.EncryptOutput, error)
	Decrypt(ctx context.Context, input *kms.DecryptInput, optFns ...func(*kms.Options)) (*kms.DecryptOutput, error)
}

// IsKMSEncrypted reports whether value is wrapped in KMS[...].
func IsKMSEncrypted(value string) bool {
	return strings.HasPrefix(value, kmsPrefix) && strings.HasSuffix(value, kmsSuffix) && len(value) > len(kmsPrefix)+len(kmsSuffix)
}

// KMSEncrypt encrypts plaintext using the given KMS key and returns a KMS[...]
// string. The key ID can be an ARN, alias, or key ID.
func KMSEncrypt(ctx context.Context, client KMSClient, keyID, plaintext string) (string, error) {
	out, err := client.Encrypt(ctx, &kms.EncryptInput{
		KeyId:     aws.String(keyID),
		Plaintext: []byte(plaintext),
	})
	if err != nil {
		return "", fmt.Errorf("kms encrypt: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(out.CiphertextBlob)
	return kmsPrefix + encoded + kmsSuffix, nil
}

// KMSDecrypt decrypts a KMS[...] value. The KMS key ID is embedded in the
// ciphertext blob, so no key ID parameter is needed.
func KMSDecrypt(ctx context.Context, client KMSClient, enc string) (string, error) {
	if !IsKMSEncrypted(enc) {
		return "", fmt.Errorf("value is not KMS-encrypted (missing KMS[...] wrapper)")
	}
	inner := enc[len(kmsPrefix) : len(enc)-len(kmsSuffix)]
	ciphertext, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	out, err := client.Decrypt(ctx, &kms.DecryptInput{
		CiphertextBlob: ciphertext,
	})
	if err != nil {
		return "", fmt.Errorf("kms decrypt: %w", err)
	}
	return string(out.Plaintext), nil
}

// LoadAWSConfig builds an aws.Config using the default credential chain with an
// optional region override from secrets.aws_region config or SEKIA_AWS_REGION.
func LoadAWSConfig(ctx context.Context, v *viper.Viper) (aws.Config, error) {
	var opts []func(*awsconfig.LoadOptions) error

	// Check sekia-specific region override.
	if region := v.GetString("secrets.aws_region"); region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("load AWS config: %w (ensure AWS credentials are configured via environment, profile, or instance role)", err)
	}
	return cfg, nil
}
