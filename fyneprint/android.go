//go:build android

package fyneprint

import (
	"errors"

	"fyne.io/fyne/v2/driver"

	"github.com/timzifer/goprint"
)

// On Android, goprint reaches the print dialog through Java; Fyne lends
// it the JVM and the Activity.
func init() {
	goprint.SetAndroidRunner(func(f func(vm, env, activity uintptr) error) error {
		return driver.RunNative(func(c any) error {
			ac, ok := c.(*driver.AndroidContext)
			if !ok {
				return errors.New("fyneprint: no Android context")
			}
			return f(ac.VM, ac.Env, ac.Ctx)
		})
	})
}
