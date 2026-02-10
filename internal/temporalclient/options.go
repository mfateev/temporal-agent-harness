// Package temporalclient provides Temporal client configuration loading
// using the SDK's envconfig contrib package.
//
// This enables configuration via environment variables (TEMPORAL_HOST_URL,
// TEMPORAL_NAMESPACE, TEMPORAL_TLS_CERT, etc.) and config files (config.toml),
// matching the pattern from temporal/samples-go/external-env-conf.
package temporalclient

import (
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/envconfig"
)

// LoadClientOptions loads Temporal client options using the envconfig system.
// This supports:
//   - Environment variables (TEMPORAL_HOST_URL, TEMPORAL_NAMESPACE, TEMPORAL_TLS_CERT, etc.)
//   - Config file (config.toml in working directory or TEMPORAL_CONFIG_FILE)
//   - Temporal Cloud connection via TEMPORAL_HOST_URL + TEMPORAL_TLS_CERT + TEMPORAL_TLS_KEY
//
// If hostPortOverride is non-empty, it overrides the host:port from envconfig.
// If namespaceOverride is non-empty, it overrides the namespace.
//
// See: github.com/temporalio/samples-go/external-env-conf
func LoadClientOptions(hostPortOverride, namespaceOverride string) (client.Options, error) {
	opts, err := envconfig.LoadClientOptions(envconfig.LoadClientOptionsRequest{})
	if err != nil {
		return client.Options{}, err
	}

	if hostPortOverride != "" {
		opts.HostPort = hostPortOverride
	}
	if namespaceOverride != "" {
		opts.Namespace = namespaceOverride
	}

	return opts, nil
}

// MustLoadClientOptions is like LoadClientOptions but panics on error.
func MustLoadClientOptions(hostPortOverride, namespaceOverride string) client.Options {
	opts, err := LoadClientOptions(hostPortOverride, namespaceOverride)
	if err != nil {
		panic("failed to load Temporal client options: " + err.Error())
	}
	return opts
}
