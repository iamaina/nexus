package config

import "gopkg.in/yaml.v3"

func unmarshalYAML(data []byte, v any) error {
	return yaml.Unmarshal(data, v)
}

func marshalYAML(v any) ([]byte, error) {
	return yaml.Marshal(v)
}
