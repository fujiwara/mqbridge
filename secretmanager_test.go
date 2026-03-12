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
