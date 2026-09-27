package agent

import (
	"context"
	"testing"

	"github.com/envplane/contracts/domain"
)

func TestGeneratedSecretGeneratorPostgresUsesApplicationAndPostgresKeys(t *testing.T) {
	data, err := NewGeneratedSecretGenerator().Generate(context.Background(), domain.SecretMaterializationItem{Generator: "postgresql-password-v1:DB_PASSWORD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(data["DB_PASSWORD"]) == 0 || len(data["POSTGRES_PASSWORD"]) == 0 {
		t.Fatalf("generated database credentials=%#v", data)
	}
	if string(data["DB_PASSWORD"]) != string(data["POSTGRES_PASSWORD"]) {
		t.Fatal("application and PostgreSQL password must be the same value")
	}
}

func TestGeneratedSecretGeneratorRejectsUnrecognisedProfile(t *testing.T) {
	if _, err := NewGeneratedSecretGenerator().Generate(context.Background(), domain.SecretMaterializationItem{Generator: "unknown:DB_PASSWORD"}); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}
