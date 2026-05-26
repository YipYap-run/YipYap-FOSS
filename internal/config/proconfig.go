package config

// ProConfig contains the DB driver (hardcoded to sqlite in FOSS).
// Checker tuning fields are present but always zero  - defaults apply.
type ProConfig struct {
	DBDriver      string
	PublicBaseURL string
	OpsToken string

	CheckerWorkers           int
	CheckerChannelSize       int
	CheckerPriorityThreshold int
	CheckerBatchSize         int
	CheckerBatchWriters      int
	CheckerFlushConcurrency  int

	// Transactional email (password resets, etc.)
	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	SMTPFrom string

	AllowPrivateTargets bool // FOSS defaults to true so self-hosters can monitor their own networks

	// AllowPrivateControlPlaneTargets governs SSRF protection for
	// control-plane URLs (CloudEvent sink_url, OIDC token_url / issuer,
	// JWKS). Distinct from AllowPrivateTargets: a FOSS deployment that
	// legitimately watches intranet monitors should NOT simultaneously
	// accept attacker-submitted sink_url pointing at the same intranet.
	// Defaults to false even in FOSS; override via
	// YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true for localhost
	// CloudEvent development.
	AllowPrivateControlPlaneTargets bool
}

func loadProConfig(c *Config) {
	c.DBDriver = "sqlite"
	// FOSS/self-hosted: allow monitoring private networks by default.
	c.AllowPrivateTargets = envBool("YIPYAP_ALLOW_PRIVATE_TARGETS", true)
	// Control-plane URLs (sink, OIDC) default-deny even in FOSS.
	c.AllowPrivateControlPlaneTargets = envBool("YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS", false)
}
