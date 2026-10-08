package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"strings"

	clusteragent "github.com/envplane/agent/agent"
)

var errReviewedProfile = errors.New("invalid reviewed recovery profile; no permissions applied")

type protectedSecretFlags []clusteragent.DatabaseCredentialSecretReference

func (p *protectedSecretFlags) String() string { return "" }
func (p *protectedSecretFlags) Set(value string) error {
	namespace, name, found := strings.Cut(value, "/")
	if !found || namespace == "" || name == "" || strings.Contains(name, "/") {
		return errReviewedProfile
	}
	*p = append(*p, clusteragent.DatabaseCredentialSecretReference{Namespace: namespace, Name: name})
	return nil
}

func validateUnambiguousJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 16 {
			return errReviewedProfile
		}
		token, err := decoder.Token()
		if err != nil {
			return errReviewedProfile
		}
		delimiter, nested := token.(json.Delim)
		if !nested {
			return nil
		}
		if delimiter != '{' && delimiter != '[' {
			return errReviewedProfile
		}
		seen := map[string]bool{}
		for decoder.More() {
			if delimiter == '{' {
				keyToken, err := decoder.Token()
				if err != nil {
					return errReviewedProfile
				}
				key, ok := keyToken.(string)
				key = strings.ToLower(key) // Go struct decoding matches case-insensitively.
				if !ok || seen[key] {
					return errReviewedProfile
				}
				seen[key] = true
			}
			if err := walk(depth + 1); err != nil {
				return errReviewedProfile
			}
		}
		closer, err := decoder.Token()
		if err != nil || (delimiter == '{' && closer != json.Delim('}')) || (delimiter == '[' && closer != json.Delim(']')) {
			return errReviewedProfile
		}
		return nil
	}
	if err := walk(0); err != nil {
		return errReviewedProfile
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errReviewedProfile
	}
	return nil
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("db-credential-recovery-profile", flag.ContinueOnError)
	flags.SetOutput(io.Discard) // Never echo untrusted file contents/flag values.
	filePath := flags.String("reviewed-bindings", "", "bounded reviewed binding JSON file, never a key or plaintext credential file")
	escrowNamespace := flags.String("escrow-namespace", "", "separate protected ciphertext-only namespace")
	saNamespace := flags.String("service-account-namespace", "", "exact ServiceAccount Pod namespace")
	saName := flags.String("service-account", "", "exact ServiceAccount name")
	keyRef := flags.String("key-ref", "", "nonempty metadata key version reference; grants no key access")
	createReviewed := flags.Bool("acknowledge-namespace-secret-create", false, "reviewed dedicated namespace Secret CREATE limitation; never applies permissions")
	var protected protectedSecretFlags
	flags.Var(&protected, "protected-secret", "repeat exact namespace/name for key/binding Secrets that must not become import targets")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *filePath == "" {
		return errReviewedProfile
	}
	file, err := os.Open(*filePath)
	if err != nil {
		return errReviewedProfile
	}
	defer func() { _ = file.Close() }()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
		return errReviewedProfile
	}
	payload, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(payload) > 1<<20 {
		return errReviewedProfile
	}
	if validateUnambiguousJSON(payload) != nil {
		return errReviewedProfile
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var authorizations []clusteragent.DatabaseCredentialAuthorization
	if decoder.Decode(&authorizations) != nil {
		return errReviewedProfile
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errReviewedProfile
	}
	profile, err := clusteragent.BuildDatabaseCredentialEscrowAccessProfile(clusteragent.DatabaseCredentialEscrowAccessProfileInput{Authorizations: authorizations, EscrowNamespace: *escrowNamespace, ServiceAccountNamespace: *saNamespace, ServiceAccountName: *saName, KeyRef: *keyRef, ProtectedSecrets: protected, NamespaceSecretCreateReviewed: *createReviewed})
	if err != nil {
		return errReviewedProfile
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(profile); err != nil {
		return errReviewedProfile
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, errReviewedProfile.Error()+"\n")
		os.Exit(1)
	}
}
