package oci

// Redact removes secret-bearing fields from API responses and last-applied.
func Redact(spec Spec) Spec {
	out := spec
	out.PullUsername = ""
	out.PullPassword = ""
	out.SecretRefs = nil
	if len(spec.SecretRefs) > 0 {
		out.SecretRefs = make([]SecretRef, 0, len(spec.SecretRefs))
		for _, s := range spec.SecretRefs {
			out.SecretRefs = append(out.SecretRefs, SecretRef{Name: s.Name, SecretID: s.SecretID})
		}
	}
	return out
}

// RedactApplied clears digest-adjacent secrets; Spec is already redacted on write.
func RedactApplied(a Applied) Applied {
	a.Spec = Redact(a.Spec)
	return a
}
