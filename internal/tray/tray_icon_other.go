//go:build !windows && !darwin && !linux

package tray

import _ "embed"

//go:embed icon.ico
var iconBytes []byte
