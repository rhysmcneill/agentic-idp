package cmd

import (
	"fmt"
	"io"
	"log/slog"
)

// masker emits a CI platform's own log-masking hints for the sensitive
// fields in a set of credentials, before those values are printed in
// whatever --format the caller asked for — so a credential never sits in
// plaintext in a build log even when a workflow uses --format=json/env
// without redirecting output.
type masker interface {
	Provider() string
	maskSecrets(w io.Writer, c credentials)
}

// maskerRegistry resolves a CI provider to its masker — same shape as
// ciauth.Registry and pkg/ci.Registry, so a new provider's masker is
// "implement masker, register it," not a factory function to edit.
type maskerRegistry struct {
	maskers map[string]masker
}

func newMaskerRegistry(maskers ...masker) *maskerRegistry {
	r := &maskerRegistry{maskers: make(map[string]masker, len(maskers))}
	for _, m := range maskers {
		r.maskers[m.Provider()] = m
	}
	return r
}

// get returns the masker registered for provider, or noMasker{} if none is —
// a provider with no native masking mechanism still gets a loud warning
// rather than silently unmasked credentials.
func (r *maskerRegistry) get(provider string) masker {
	if m, ok := r.maskers[provider]; ok {
		return m
	}
	return noMasker{}
}

// githubActionsMasker uses GitHub's own workflow command, parsed from a
// step's stdout, to redact a value everywhere it appears in the log from
// this point on — the same mechanism aws-actions/configure-aws-credentials
// uses for the same reason.
type githubActionsMasker struct{}

func (githubActionsMasker) Provider() string { return "github_actions" }

func (githubActionsMasker) maskSecrets(w io.Writer, c credentials) {
	for _, value := range []string{c.SecretAccessKey, c.SessionToken} { // pragma: allowlist secret
		if value == "" {
			continue
		}
		_, _ = fmt.Fprintln(w, "::add-mask::"+value)
	}
}

// noMasker is used for a CI provider with no registered masker — it warns
// once, loudly, rather than silently pretending credentials are protected.
type noMasker struct{}

func (noMasker) Provider() string { return "" }

func (noMasker) maskSecrets(io.Writer, credentials) {
	slog.Warn("this CI platform has no native log-masking command idpctl knows about — printed credentials will not be automatically redacted from the build log; redirect this command's output (e.g. into an AWS credential_process file) rather than leaving it as a bare step")
}

// maskers is every masker idpctl ci auth knows about. Adding one for a new
// CI provider means implementing masker and adding it here, nothing else.
var maskers = newMaskerRegistry(githubActionsMasker{})
