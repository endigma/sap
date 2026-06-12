module github.com/endigma/sap/viewer/sap-web

// go >= 1.26.3 is forced by the released sap v0.1.0 dependency; go mod tidy
// restores it if lowered, and go.work must list at least this version.
go 1.26.3

require (
	github.com/a-h/templ v0.3.1020
	github.com/alecthomas/chroma/v2 v2.20.0
	github.com/endigma/sap v0.1.0
)

require (
	github.com/dlclark/regexp2 v1.11.5 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
