//go:build darwin

package main

import _ "embed"

// speechHelper is the executable built from tools/speech-helper (make
// speech-helper), which drives macOS's on-device speech recognition. It is
// written to the data folder's runtime/ on first use.
//
//go:embed assets/speech-helper
var speechHelper []byte
