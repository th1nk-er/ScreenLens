package tray

import (
	"context"

	"github.com/getlantern/systray"
)

type Actions struct {
	Capture func()
	Reload  func()
	Exit    func()
}

// Run owns the optional tray lifecycle. It communicates with the same
// workflow callbacks as the hotkey and Telegram command handlers.
func Run(ctx context.Context, title string, actions Actions) {
	done := make(chan struct{})
	systray.Run(func() {
		systray.SetIcon(iconBytes)
		systray.SetTitle(title)
		systray.SetTooltip(title)
		capture := systray.AddMenuItem("Capture now", "Capture and analyze the screen")
		reload := systray.AddMenuItem("Reload config", "Reload ScreenLens configuration")
		systray.AddSeparator()
		exit := systray.AddMenuItem("Exit", "Exit ScreenLens")

		go func() {
			for {
				select {
				case <-capture.ClickedCh:
					if actions.Capture != nil {
						actions.Capture()
					}
				case <-reload.ClickedCh:
					if actions.Reload != nil {
						actions.Reload()
					}
				case <-exit.ClickedCh:
					if actions.Exit != nil {
						actions.Exit()
					}
					systray.Quit()
				case <-ctx.Done():
					systray.Quit()
					return
				}
			}
		}()
	}, func() {
		close(done)
	})
	<-done
}
