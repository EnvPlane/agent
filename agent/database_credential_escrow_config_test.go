package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDatabaseEscrowMountedBindingAndKeyAreStrict(t *testing.T) {
	_, _, _, refs, command := newEscrowFixture(t, "mysql")
	dir := t.TempDir()
	mounted := MountedDatabaseEscrowReferences{KeyFile: filepath.Join(dir, "key"), BindingFile: filepath.Join(dir, "bindings"), KeyRef: "operator-key-v1", ClusterID: "cluster"}
	if err := os.WriteFile(mounted.KeyFile, refs.key, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "duplicate", "foreign cluster", "unknown field", "trailing", "empty"} {
		t.Run(mode, func(t *testing.T) {
			auth := refs.auth
			if mode == "foreign cluster" {
				auth.Binding.ClusterID = "different"
			}
			items := []DatabaseCredentialAuthorization{auth}
			if mode == "duplicate" {
				items = append(items, auth)
			}
			if mode == "empty" {
				items = nil
			}
			payload, err := json.Marshal(items)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown field" {
				payload = []byte(`[{"unknown":true}]`)
			}
			if mode == "trailing" {
				payload = append(payload, []byte(`{}`)...)
			}
			if err := os.WriteFile(mounted.BindingFile, payload, 0600); err != nil {
				t.Fatal(err)
			}
			_, err = mounted.ResolveDatabaseCredentialBinding(context.Background(), command, command.Plan.Items[0])
			if (err == nil) != (mode == "valid") {
				t.Fatal("invalid binding configuration accepted")
			}
		})
	}
	key, err := mounted.ResolveDatabaseCredentialKey(context.Background(), "operator-key-v1")
	if err != nil || len(key) != 32 {
		t.Fatal("mounted external key unavailable")
	}
	clearMaterialBytes(key)
	if _, err := mounted.ResolveDatabaseCredentialKey(context.Background(), "unknown"); !errors.Is(err, ErrDatabaseCredentialRecovery) {
		t.Fatal("unknown key reference accepted")
	}
}

func TestDatabaseEscrowOptInDefaultsAndIncompleteConfiguration(t *testing.T) {
	for _, value := range []string{"", "false", "true", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ENVPLANE_DB_CREDENTIAL_ESCROW_ENABLED", value)
			t.Setenv("ENVPLANE_DB_CREDENTIAL_ESCROW_KEY_FILE", "")
			err := ConfigureDatabaseCredentialEscrowFromEnv(nil, nil, "cluster")
			if (err == nil) != (value == "" || value == "false") {
				t.Fatal("invalid opt-in configuration accepted")
			}
		})
	}
}
