package mqbridge

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	jsonnet "github.com/google/go-jsonnet"
	"github.com/google/go-jsonnet/ast"
	saclient "github.com/sacloud/saclient-go"
	secretmanager "github.com/sacloud/secretmanager-api-go"
	v1 "github.com/sacloud/secretmanager-api-go/apis/v1"
)

func secretNativeFunction(ctx context.Context) *jsonnet.NativeFunction {
	return &jsonnet.NativeFunction{
		Name:   "secret",
		Params: ast.Identifiers{"vault_id", "name"},
		Func: func(args []any) (any, error) {
			vaultID, ok := args[0].(string)
			if !ok {
				return nil, fmt.Errorf("secret: vault_id must be a string")
			}
			name, ok := args[1].(string)
			if !ok {
				return nil, fmt.Errorf("secret: name must be a string")
			}
			return unveilSecret(ctx, vaultID, name)
		},
	}
}

// parseSecretName parses a name string into a secret name and optional version.
// Format: "name" (latest) or "name:version" (specific version).
func parseSecretName(s string) (string, v1.OptNilInt, error) {
	name, versionStr, hasVersion := strings.Cut(s, ":")
	if !hasVersion {
		return name, v1.OptNilInt{}, nil
	}
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		return "", v1.OptNilInt{}, fmt.Errorf("secret: invalid version %q: %w", versionStr, err)
	}
	return name, v1.NewOptNilInt(version), nil
}

func unveilSecret(ctx context.Context, vaultID, nameWithVersion string) (string, error) {
	name, version, err := parseSecretName(nameWithVersion)
	if err != nil {
		return "", err
	}
	sa := &saclient.Client{}
	if err := sa.SetEnviron(os.Environ()); err != nil {
		return "", fmt.Errorf("secret: failed to set environment: %w", err)
	}
	if err := sa.Populate(); err != nil {
		return "", fmt.Errorf("secret: failed to populate client: %w", err)
	}
	client, err := secretmanager.NewClient(sa)
	if err != nil {
		return "", fmt.Errorf("secret: failed to create client: %w", err)
	}
	secOp := secretmanager.NewSecretOp(client, vaultID)
	result, err := secOp.Unveil(ctx, v1.Unveil{Name: name, Version: version})
	if err != nil {
		return "", fmt.Errorf("secret: failed to unveil %q: %w", nameWithVersion, err)
	}
	return result.Value, nil
}
