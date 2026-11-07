// Package api embeds the reviewed engine contract for development tooling.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
