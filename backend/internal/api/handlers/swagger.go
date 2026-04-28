package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/swaggo/swag"
)

// SwaggerFilterHandler serves a filtered swagger.json that only includes
// operations matching the allowed tags (e.g. Patients, ECG, Exports).
func SwaggerFilterHandler(allowedTags map[string]bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		doc := swag.GetSwagger("swagger")
		if doc == nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "swagger spec not found"})
		}

		spec := doc.ReadDoc()
		var raw map[string]any
		if err := json.Unmarshal([]byte(spec), &raw); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "invalid spec"})
		}

		paths, _ := raw["paths"].(map[string]any)
		filtered := make(map[string]any)
		for path, methods := range paths {
			methodMap, ok := methods.(map[string]any)
			if !ok {
				continue
			}
			kept := make(map[string]any)
			for method, op := range methodMap {
				opMap, ok := op.(map[string]any)
				if !ok {
					continue
				}
				tags, _ := opMap["tags"].([]any)
				for _, t := range tags {
					if s, ok := t.(string); ok && allowedTags[s] {
						kept[method] = op
						break
					}
				}
			}
			if len(kept) > 0 {
				filtered[path] = kept
			}
		}
		raw["paths"] = filtered

		tagList := make([]map[string]string, 0, len(allowedTags))
		for t := range allowedTags {
			tagList = append(tagList, map[string]string{"name": t})
		}
		raw["tags"] = tagList

		return c.JSON(http.StatusOK, raw)
	}
}
