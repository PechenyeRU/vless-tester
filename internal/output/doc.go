// Package output builds the public artifacts published to GitHub: a base64
// subscription of renamed working links, a public JSON (country, sequence name,
// speed only), and a README table. Artifacts must never leak inner workings
// (no worker/vantage/diagnostics). Push targets a separate, configurable
// repository. See T0.7 in PLAN.md.
package output
