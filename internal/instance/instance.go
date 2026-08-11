package instance

import "errors"

var ErrAlreadyRunning = errors.New("another ScreenLens instance is already running")
