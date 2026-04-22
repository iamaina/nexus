package nexus

import (
	"github.com/iamaina/nexus/internal/config"
	"github.com/iamaina/nexus/internal/models"
)

// buildSearchFilter constructs a SearchFilter from the user's explicit flags and
// the source defaults declared in config.yaml.
//
// Priority:
//  1. source non-empty → restrict to that source; ExcludeNames bypassed
//  2. category non-empty → restrict to sources in that category; ExcludeNames bypassed
//  3. neither → apply ExcludeNames from sources with search_by_default: false
func buildSearchFilter(cfg *config.Config, source, category string) models.SearchFilter {
	f := models.SearchFilter{Source: source}

	if category != "" {
		for _, s := range cfg.Sources {
			if s.Category == category {
				f.IncludeNames = append(f.IncludeNames, s.Name)
			}
		}
		for _, u := range cfg.URLs {
			if u.Category == category {
				f.IncludeNames = append(f.IncludeNames, u.Name)
			}
		}
		return f // category takes precedence — no further exclusions applied
	}

	if source != "" {
		return f // explicit source — no exclusions needed
	}

	// Default: exclude any source with search_by_default: false
	for _, s := range cfg.Sources {
		if !s.IsSearchDefault() {
			f.ExcludeNames = append(f.ExcludeNames, s.Name)
		}
	}
	for _, u := range cfg.URLs {
		if !u.IsSearchDefault() {
			f.ExcludeNames = append(f.ExcludeNames, u.Name)
		}
	}
	return f
}
