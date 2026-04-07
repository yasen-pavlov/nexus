package connector

import "fmt"

type Factory func() Connector

var registry = map[string]Factory{}

func Register(connectorType string, factory Factory) {
	registry[connectorType] = factory
}

func Create(connectorType string) (Connector, error) {
	factory, ok := registry[connectorType]
	if !ok {
		return nil, fmt.Errorf("connector: unknown type %q", connectorType)
	}
	return factory(), nil
}

func List() []string {
	types := make([]string, 0, len(registry))
	for t := range registry {
		types = append(types, t)
	}
	return types
}
