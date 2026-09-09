package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestFrenchPromptTemplateMetadata(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "config", "prompt_templates", "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	for _, path := range files {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			var file struct {
				Templates []PromptTemplate `yaml:"templates"`
			}
			require.NoError(t, yaml.Unmarshal(data, &file))
			require.NotEmpty(t, file.Templates)
			localized := LocalizeTemplates(file.Templates, "fr-FR")
			for i, source := range file.Templates {
				french := source.I18n["fr-FR"]
				require.NotEmpty(t, french.Name, source.ID)
				require.NotEmpty(t, french.Description, source.ID)
				assert.Equal(t, french.Name, localized[i].Name)
				assert.Equal(t, french.Description, localized[i].Description)
				// Locale selection must change metadata only, including for templates
				// with a separate user prompt and runtime {{language}} parameters.
				localized[i].Name = source.Name
				localized[i].Description = source.Description
				assert.Equal(t, source, localized[i], source.ID)
			}
		})
	}
}
