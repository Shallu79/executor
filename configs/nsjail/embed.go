// Package nsjailcfg embeds the base nsjail config template into the worker
// binary so no config files need to be deployed alongside it.
package nsjailcfg

import _ "embed"

// BaseTemplate is the Go text/template for the per-job nsjail config.
//
//go:embed base.template.cfg
var BaseTemplate string
