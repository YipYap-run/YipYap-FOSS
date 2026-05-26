// Package gen parses the CloudEvent catalog YAML into typed structs consumed by code generators.
package gen

// Catalog is the parsed view of internal/cloudevents/catalog/*.yaml.
type Catalog struct {
	Events []Event
}

// Event describes a single CloudEvent type in the yipyap catalog.
type Event struct {
	Type            string  `yaml:"type"`
	Direction       string  `yaml:"direction"` // "out", "in", "reply"
	Summary         string  `yaml:"summary"`
	SubjectTemplate string  `yaml:"subject_template"`
	Data            []Field `yaml:"data"`
}

// Field is one attribute of an event's data payload.
type Field struct {
	Name        string   `yaml:"name"`
	Type        string   `yaml:"type"` // "string", "int", "bool", "object", "array"
	Required    bool     `yaml:"required"`
	Format      string   `yaml:"format,omitempty"`   // "ulid", "rfc3339", "uri", ...
	Enum        []string `yaml:"enum,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Items       *Field   `yaml:"items,omitempty"`    // for arrays
}

var validDirections = map[string]struct{}{"out": {}, "in": {}, "reply": {}}
