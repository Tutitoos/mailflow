package translations

import "maps"

type Catalog struct {
	defaultLocale string
	values        map[string]map[string]string
}

func NewCatalog() *Catalog {
	return &Catalog{
		defaultLocale: "en",
		values: map[string]map[string]string{
			"en": {"app.name": "Mailflow", "status.healthy": "All systems operational"},
			"es": {"app.name": "Mailflow", "status.healthy": "Todos los sistemas están operativos"},
		},
	}
}

func (c *Catalog) Locale(locale string) map[string]string {
	result := maps.Clone(c.values[c.defaultLocale])
	if selected, ok := c.values[locale]; ok {
		maps.Copy(result, selected)
	}
	return result
}
