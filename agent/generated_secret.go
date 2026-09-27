package agent

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/envplane/contracts/domain"
)

const generatedSecretBytes = 32

// GeneratedSecretGenerator creates per-environment values only on the target
// Agent. Values never pass through the control plane or GitOps repository.
type GeneratedSecretGenerator struct{}

func NewGeneratedSecretGenerator() GeneratedSecretGenerator { return GeneratedSecretGenerator{} }

func (GeneratedSecretGenerator) Generate(_ context.Context, item domain.SecretMaterializationItem) (map[string][]byte, error) {
	profile, key := generatedSecretProfile(item.Generator)
	if profile == "" || key == "" {
		return nil, fmt.Errorf("unsupported generated secret profile %q", item.Generator)
	}
	value := make([]byte, generatedSecretBytes)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate secret bytes: %w", err)
	}
	password := []byte(base64.RawURLEncoding.EncodeToString(value))
	clearMaterialBytes(value)
	data := map[string][]byte{key: password}
	for _, engineKey := range generatedDatabasePasswordKeys(profile) {
		if engineKey != key {
			data[engineKey] = append([]byte(nil), password...)
		}
	}
	return data, nil
}

func generatedSecretProfile(raw string) (string, string) {
	profile, key, found := strings.Cut(strings.TrimSpace(raw), ":")
	if !found || strings.TrimSpace(key) == "" {
		return "", ""
	}
	profile, key = strings.TrimSpace(profile), strings.TrimSpace(key)
	switch profile {
	case "random-password-v1", "postgresql-password-v1", "mysql-password-v1", "mariadb-password-v1", "mongodb-password-v1", "redis-password-v1":
		return profile, key
	default:
		return "", ""
	}
}

func generatedDatabasePasswordKeys(profile string) []string {
	switch profile {
	case "postgresql-password-v1":
		return []string{"POSTGRES_PASSWORD"}
	case "mysql-password-v1", "mariadb-password-v1":
		return []string{"MYSQL_PASSWORD"}
	case "mongodb-password-v1":
		return []string{"MONGO_INITDB_ROOT_PASSWORD"}
	case "redis-password-v1":
		return []string{"REDIS_PASSWORD"}
	default:
		return nil
	}
}
