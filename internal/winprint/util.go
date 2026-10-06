//go:build windows

package winprint

import "golang.org/x/sys/windows"

func syscallUTF16(s string) (*uint16, error) { return windows.UTF16PtrFromString(s) }
