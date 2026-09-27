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
	if profile == "postgresql-password-v1" && key != "POSTGRES_PASSWORD" {
		data["POSTGRES_PASSWORD"] = append([]byte(nil), password...)
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
	case "random-password-v1", "postgresql-password-v1":
		return profile, key
	default:
		return "", ""
	}
}
