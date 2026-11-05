package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/spf13/viper"
)

const resolveTimeout = 30 * time.Second

// ResolveViperConfig walks all Viper string keys and decrypts any KMS[...]
// values in-place via the AWS KMS API. The AWS client is only created when at
// least one KMS value is present. Returns an error if any value cannot be
// decrypted, so a misconfigured secret fails startup instead of being used as
// a literal string.
func ResolveViperConfig(v *viper.Viper) error {
	var kmsKeys []string
	for _, key := range v.AllKeys() {
		if IsKMSEncrypted(v.GetString(key)) {
			kmsKeys = append(kmsKeys, key)
		}
	}
	if len(kmsKeys) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()

	awsCfg, err := LoadAWSConfig(ctx, v)
	if err != nil {
		return fmt.Errorf("resolve AWS config for KMS values: %w", err)
	}

	kmsClient := kms.NewFromConfig(awsCfg)
	for _, key := range kmsKeys {
		plaintext, err := KMSDecrypt(ctx, kmsClient, v.GetString(key))
		if err != nil {
			return fmt.Errorf("decrypt config key %q: %w", key, err)
		}
		v.Set(key, plaintext)
	}
	return nil
}
