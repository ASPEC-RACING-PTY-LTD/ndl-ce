package license

const (
	CapIdentityOIDC     = "identity.oidc"
	CapIdentitySAML     = "identity.saml"
	CapIdentityLDAP     = "identity.ldap"
	AuditExport         = "audit.export"
	CapFleetInventory   = "fleet.inventory"
	CapPolicyAdvanced   = "policy.advanced"
	CapArtifactsSigned  = "artifacts.signed"
	CapAdminEnterprise  = "admin.enterprise"
	CapComplianceOffbox = "compliance.offbox"
)

func Catalog() []string {
	return []string{
		CapIdentityOIDC, CapIdentitySAML, CapIdentityLDAP, AuditExport,
		CapFleetInventory, CapPolicyAdvanced, CapArtifactsSigned,
		CapAdminEnterprise, CapComplianceOffbox,
	}
}

func knownCapability(id string) bool {
	for _, c := range Catalog() {
		if c == id {
			return true
		}
	}
	return false
}
