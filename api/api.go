// Package api contains the OpenAPI specification of monsieur-logger.
package api

import _ "embed"

// OpenAPISpec is the OpenAPI 3 document served at /swagger/openapi.yaml.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
