package web

import _ "embed"

//go:embed index.html
var IndexHTML string

//go:embed delete.html
var DeleteHTML string

//go:embed favicon.svg
var FaviconSVG []byte

//go:embed favicon.ico
var FaviconICO []byte
