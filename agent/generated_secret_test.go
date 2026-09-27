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

func TestGeneratedSecretGeneratorUsesDatabaseSpecificPasswordKeys(t *testing.T) {
	tests := []struct {
		name      string
		generator string
		key       string
		engineKey string
	}{
		{name: "PostgreSQL", generator: "postgresql-password-v1", key: "DB_PASSWORD", engineKey: "POSTGRES_PASSWORD"},
		{name: "MySQL", generator: "mysql-password-v1", key: "DB_PASSWORD", engineKey: "MYSQL_PASSWORD"},
		{name: "MariaDB", generator: "mariadb-password-v1", key: "DB_PASSWORD", engineKey: "MYSQL_PASSWORD"},
		{name: "MongoDB", generator: "mongodb-password-v1", key: "DB_PASSWORD", engineKey: "MONGO_INITDB_ROOT_PASSWORD"},
		{name: "Redis", generator: "redis-password-v1", key: "DB_PASSWORD", engineKey: "REDIS_PASSWORD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := NewGeneratedSecretGenerator().Generate(context.Background(), domain.SecretMaterializationItem{Generator: tt.generator + ":" + tt.key})
			if err != nil {
				t.Fatal(err)
			}
			if len(data[tt.key]) == 0 || len(data[tt.engineKey]) == 0 {
				t.Fatalf("generated database credentials=%#v", data)
			}
			if string(data[tt.key]) != string(data[tt.engineKey]) {
				t.Fatalf("application and %s password must be the same value", tt.name)
			}
		})
	}
}

func TestGeneratedSecretGeneratorRejectsUnrecognisedProfile(t *testing.T) {
	if _, err := NewGeneratedSecretGenerator().Generate(context.Background(), domain.SecretMaterializationItem{Generator: "unknown:DB_PASSWORD"}); err == nil {
		t.Fatal("expected unsupported profile error")
	}
}
