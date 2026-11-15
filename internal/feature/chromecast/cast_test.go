package chromecast

import (
	"slices"
	"testing"
)

func TestEngineArgsDropTheWebSecurityExceptionsOnlyWhenQuiet(t *testing.T) {
	const insecure = "--allow-running-insecure-content"
	if !slices.Contains(engineArgs(false), insecure) {
		t.Errorf("engineArgs(open) lost what Google-hosted receiver pages need: %q",
			engineArgs(false))
	}
	if slices.Contains(engineArgs(true), insecure) {
		t.Errorf("engineArgs(quiet) allows insecure content: %q", engineArgs(true))
	}
}
