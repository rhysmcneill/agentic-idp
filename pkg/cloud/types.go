// Package cloud defines a provider-agnostic interface for minting scoped
// cloud credentials. AWS is the only implementation in v1; the interface is
// generalised now so GCP/Azure are additive later rather than a rewrite.
package cloud

// Provider identifies a cloud provider.
type Provider string

// Provider identifiers this package recognises. Only ProviderAWS has an
// implementation in v1 (worker/internal/broker/aws). GCP and Azure are
// deferred, not abandoned.
const (
	ProviderAWS   Provider = "aws"
	ProviderGCP   Provider = "gcp"
	ProviderAzure Provider = "azure"
)
