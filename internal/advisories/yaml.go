package advisories

import "gopkg.in/yaml.v3"

// unmarshalYAML decodes YAML bytes into dst using gopkg.in/yaml.v3.
func unmarshalYAML(data []byte, dst interface{}) error {
	return yaml.Unmarshal(data, dst)
}
