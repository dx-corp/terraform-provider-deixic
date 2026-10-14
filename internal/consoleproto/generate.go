// Package consoleproto holds Go bindings for the wire-compatible console.v1
// subset in console_subset.protodef. The definition keeps a non-.proto
// extension because it is not part of the repository's buf module.
//
// Regenerate with protoc and protoc-gen-go v1.36.11 on PATH:
//
//go:generate sh -c "cp console_subset.protodef console_subset.proto && protoc -I . --go_out=. --go_opt=paths=source_relative console_subset.proto; rm -f console_subset.proto"
package consoleproto
