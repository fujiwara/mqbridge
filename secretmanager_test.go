package mqbridge

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fujiwara/sakura-secrets-cli/localserver"
	saclient "github.com/sacloud/saclient-go"
	secretmanager "github.com/sacloud/secretmanager-api-go"
	v1 "github.com/sacloud/secretmanager-api-go/apis/v1"
)

const secretManagerAPIPrefix = "/api/cloud/1.1"

func setupSecretManagerLocalServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := localserver.NewServer(secretManagerAPIPrefix)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	t.Setenv("SAKURA_API_ROOT_URL", ts.URL+secretManagerAPIPrefix)
	t.Setenv("SAKURA_ACCESS_TOKEN", "dummy")
	t.Setenv("SAKURA_ACCESS_TOKEN_SECRET", "dummy")
	return ts
}

func createTestSecret(t *testing.T, serverURL, vaultID, name, value string) {
	t.Helper()
	var sa saclient.Client
	if err := sa.SetEnviron([]string{
		"SAKURA_API_ROOT_URL=" + serverURL + secretManagerAPIPrefix,
		"SAKURA_ACCESS_TOKEN=dummy",
		"SAKURA_ACCESS_TOKEN_SECRET=dummy",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sa.Populate(); err != nil {
		t.Fatal(err)
	}
	client, err := secretmanager.NewClient(&sa)
	if err != nil {
		t.Fatal(err)
	}
	secOp := secretmanager.NewSecretOp(client, vaultID)
	if _, err := secOp.Create(t.Context(), v1.CreateSecret{Name: name, Value: value}); err != nil {
		t.Fatal(err)
	}
}

func TestParseSecretName(t *testing.T) {
	tests := []struct {
		input       string
		wantName    string
		wantVersion int
		wantSet     bool
		wantErr     bool
	}{
		{input: "my-secret", wantName: "my-secret", wantSet: false},
		{input: "my-secret:1", wantName: "my-secret", wantVersion: 1, wantSet: true},
		{input: "my-secret:42", wantName: "my-secret", wantVersion: 42, wantSet: true},
		{input: "my-secret:abc", wantErr: true},
		{input: "my-secret:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, version, err := parseSecretName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if version.IsSet() != tt.wantSet {
				t.Errorf("version.IsSet() = %v, want %v", version.IsSet(), tt.wantSet)
			}
			if tt.wantSet && version.Value != tt.wantVersion {
				t.Errorf("version.Value = %d, want %d", version.Value, tt.wantVersion)
			}
		})
	}
}

func TestSecretNativeFunction(t *testing.T) {
	fn := secretNativeFunction(t.Context())
	if fn.Name != "secret" {
		t.Errorf("expected Name %q, got %q", "secret", fn.Name)
	}
	if len(fn.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(fn.Params))
	}
	if string(fn.Params[0]) != "vault_id" {
		t.Errorf("expected first param %q, got %q", "vault_id", fn.Params[0])
	}
	if string(fn.Params[1]) != "name" {
		t.Errorf("expected second param %q, got %q", "name", fn.Params[1])
	}
}

func TestUnveilSecret(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault"
	secretName := "test-secret"
	secretValue := "s3cret-value-12345"

	createTestSecret(t, ts.URL, vaultID, secretName, secretValue)

	got, err := unveilSecret(t.Context(), vaultID, secretName)
	if err != nil {
		t.Fatal(err)
	}
	if got != secretValue {
		t.Errorf("expected %q, got %q", secretValue, got)
	}
}

func TestUnveilSecretMultiple(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault-multi"
	secrets := map[string]string{
		"key-1": "value-one",
		"key-2": "value-two",
		"key-3": "value-three",
	}
	for name, value := range secrets {
		createTestSecret(t, ts.URL, vaultID, name, value)
	}

	for name, want := range secrets {
		got, err := unveilSecret(t.Context(), vaultID, name)
		if err != nil {
			t.Fatalf("unveilSecret(%q): %v", name, err)
		}
		if got != want {
			t.Errorf("unveilSecret(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestUnveilSecretWithVersion(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault-ver"
	secretName := "versioned-secret"

	// Create version 1
	createTestSecret(t, ts.URL, vaultID, secretName, "value-v1")
	// Update to create version 2
	createTestSecret(t, ts.URL, vaultID, secretName, "value-v2")

	// Without version: get latest
	got, err := unveilSecret(t.Context(), vaultID, secretName)
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-v2" {
		t.Errorf("latest = %q, want %q", got, "value-v2")
	}

	// With version 1
	got, err = unveilSecret(t.Context(), vaultID, secretName+":1")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-v1" {
		t.Errorf("version 1 = %q, want %q", got, "value-v1")
	}

	// With version 2
	got, err = unveilSecret(t.Context(), vaultID, secretName+":2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "value-v2" {
		t.Errorf("version 2 = %q, want %q", got, "value-v2")
	}
}

func TestSecretNativeFunctionCall(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault-fn"
	secretName := "api-key"
	secretValue := "fn-test-value"

	createTestSecret(t, ts.URL, vaultID, secretName, secretValue)

	fn := secretNativeFunction(t.Context())
	result, err := fn.Func([]any{vaultID, secretName})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if got != secretValue {
		t.Errorf("expected %q, got %q", secretValue, got)
	}
}

func TestSecretInJsonnetConfig(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault-jsonnet"
	secretName := "simplemq-api-key"
	secretValue := "jsonnet-secret-value"

	createTestSecret(t, ts.URL, vaultID, secretName, secretValue)

	configContent := `local secret = std.native('secret');
{
  rabbitmq: {
    url: 'amqp://localhost:5672/',
  },
  bridges: [
    {
      from: {
        rabbitmq: {
          queue: 'q',
          exchange: 'ex',
          exchange_type: 'topic',
          routing_key: '#',
        },
      },
      to: [
        {
          simplemq: {
            queue: 'dest',
            api_key: secret('` + vaultID + `', '` + secretName + `'),
          },
        },
      ],
    },
  ],
}
`
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.jsonnet")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(t.Context(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	got := cfg.Bridges[0].To[0].SimpleMQ.APIKey
	if got != secretValue {
		t.Errorf("api_key = %q, want %q", got, secretValue)
	}
}

func TestSecretWithVersionInJsonnetConfig(t *testing.T) {
	ts := setupSecretManagerLocalServer(t)

	vaultID := "test-vault-jsonnet-ver"
	secretName := "simplemq-api-key"

	createTestSecret(t, ts.URL, vaultID, secretName, "old-key")
	createTestSecret(t, ts.URL, vaultID, secretName, "new-key")

	configContent := fmt.Sprintf(`local secret = std.native('secret');
{
  rabbitmq: {
    url: 'amqp://localhost:5672/',
  },
  bridges: [
    {
      from: {
        rabbitmq: {
          queue: 'q',
          exchange: 'ex',
          exchange_type: 'topic',
          routing_key: '#',
        },
      },
      to: [
        {
          simplemq: {
            queue: 'dest-latest',
            api_key: secret('%s', '%s'),
          },
        },
        {
          simplemq: {
            queue: 'dest-v1',
            api_key: secret('%s', '%s:1'),
          },
        },
      ],
    },
  ],
}
`, vaultID, secretName, vaultID, secretName)

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.jsonnet")
	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(t.Context(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}

	gotLatest := cfg.Bridges[0].To[0].SimpleMQ.APIKey
	if gotLatest != "new-key" {
		t.Errorf("latest api_key = %q, want %q", gotLatest, "new-key")
	}
	gotV1 := cfg.Bridges[0].To[1].SimpleMQ.APIKey
	if gotV1 != "old-key" {
		t.Errorf("v1 api_key = %q, want %q", gotV1, "old-key")
	}
}
