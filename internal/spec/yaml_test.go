package spec

import (
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestYAMLDocumentsAreValid(t *testing.T) {
	paths := []string{
		filepath.Join("..", "..", "docker-compose.yml"),
		filepath.Join("..", "..", "api", "openapi.yaml"),
		filepath.Join("..", "..", ".github", "workflows", "ci.yml"),
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var document any
			if err := yaml.Unmarshal(contents, &document); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
		})
	}
}
