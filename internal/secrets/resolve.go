package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/spf13/viper"
)

const resolveTimeout = 30 * time.Second

// ResolveViperConfig walks all Viper string keys and resolves any encrypted or
// referenced values in-place. It handles three formats:
//   - ENC[...] — age encryption (decrypted with a local age identity)
//   - KMS[...] — AWS KMS encryption (decrypted via the KMS API)
//   - ASM[...] — AWS Secrets Manager reference (fetched via the SM API)
//
// Backends are lazily initialized: age identity is only resolved if ENC values
// exist, and AWS clients are only created if KMS or ASM values exist. Returns
// an error if any value cannot be resolved.
func ResolveViperConfig(v *viper.Viper) error {
	// Classify values by backend.
	var encKeys, kmsKeys, asmKeys []string
	for _, key := range v.AllKeys() {
		val := v.GetString(key)
		switch {
		case IsEncrypted(val):
			encKeys = append(encKeys, key)
		case IsKMSEncrypted(val):
			kmsKeys = append(kmsKeys, key)
		case IsASMReference(val):
			asmKeys = append(asmKeys, key)
		}
	}

	// Nothing to resolve.
	if len(encKeys) == 0 && len(kmsKeys) == 0 && len(asmKeys) == 0 {
		return nil
	}

	// Resolve age-encrypted values.
	if len(encKeys) > 0 {
		identities, err := ResolveIdentity(v)
		if err != nil {
			return fmt.Errorf("resolve age identity: %w", err)
		}
		if identities == nil {
			return fmt.Errorf("config contains ENC[...] values but no age identity is configured; set %s, %s, or secrets.identity", EnvAgeKey, EnvAgeKeyFile)
		}
		for _, key := range encKeys {
			plaintext, err := Decrypt(v.GetString(key), identities...)
			if err != nil {
				return fmt.Errorf("decrypt config key %q: %w", key, err)
			}
			v.Set(key, plaintext)
		}
	}

	// Resolve AWS-backed values (KMS and/or ASM).
	if len(kmsKeys) > 0 || len(asmKeys) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
		defer cancel()

		awsCfg, err := LoadAWSConfig(ctx, v)
		if err != nil {
			return fmt.Errorf("resolve AWS config for KMS/ASM values: %w", err)
		}

		if len(kmsKeys) > 0 {
			kmsClient := kms.NewFromConfig(awsCfg)
			for _, key := range kmsKeys {
				plaintext, err := KMSDecrypt(ctx, kmsClient, v.GetString(key))
				if err != nil {
					return fmt.Errorf("decrypt config key %q: %w", key, err)
				}
				v.Set(key, plaintext)
			}
		}

		if len(asmKeys) > 0 {
			smClient := secretsmanager.NewFromConfig(awsCfg)
			for _, key := range asmKeys {
				plaintext, err := ASMFetch(ctx, smClient, v.GetString(key))
				if err != nil {
					return fmt.Errorf("resolve config key %q: %w", key, err)
				}
				v.Set(key, plaintext)
			}
		}
	}

	return nil
}
