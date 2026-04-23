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
}

func loadProConfig(c *Config) {
	c.DBDriver = "sqlite"
	// FOSS/self-hosted: allow monitoring private networks by default.
	c.AllowPrivateTargets = envBool("YIPYAP_ALLOW_PRIVATE_TARGETS", true)
}
