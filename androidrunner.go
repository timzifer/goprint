package goprint

import "sync"

// AndroidRunner runs f with the app's Java VM (JavaVM*), a JNIEnv* attached
// to the calling thread and a global reference to the app's Activity
// (android.content.Context), and returns f's error.
type AndroidRunner func(f func(vm, env, activity uintptr) error) error

var androidRunner struct {
	sync.Mutex
	run AndroidRunner
}

// SetAndroidRunner tells goprint how to reach Java on Android, where the
// print dialog is Android's PrintManager. The package
// [github.com/timzifer/goprint/fyneprint] installs one for Fyne; without
// a runner, Dialog returns [ErrNoDialog]. Other platforms ignore it.
func SetAndroidRunner(run AndroidRunner) {
	androidRunner.Lock()
	androidRunner.run = run
	androidRunner.Unlock()
}

func currentAndroidRunner() AndroidRunner {
	androidRunner.Lock()
	defer androidRunner.Unlock()
	return androidRunner.run
}
