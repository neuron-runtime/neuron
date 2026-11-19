package capabilityruntimev1

// SuspendFeature is the InitializeResponse.features value a capability runtime
// advertises to opt into the additive suspend/resume extension.
//
// It is declared here, beside the generated contract, so the Go SDK and the
// N.O.R.E. client share one spelling of the negotiated feature.
const SuspendFeature = "suspend"

// HasFeature reports whether features contains feature. Feature names are
// compared exactly: negotiation is opt-in, so an absent or empty list means
// the feature is not supported.
func HasFeature(features []string, feature string) bool {
	for _, f := range features {
		if f == feature {
			return true
		}
	}
	return false
}
