//go:build windows

package com

import (
	"context"
	"fmt"
	"unsafe"
)

// IIDIAsyncInfo is the IID of Windows.Foundation.IAsyncInfo.
var IIDIAsyncInfo = MustGUID("00000036-0000-0000-C000-000000000046")

// AsyncStatus values of IAsyncInfo.
const (
	AsyncStarted   = 0
	AsyncCompleted = 1
	AsyncCanceled  = 2
	AsyncError     = 3
)

// IAsyncInfo vtable slots (after IInspectable).
const (
	asyncInfoStatus    = 7
	asyncInfoErrorCode = 8
	asyncInfoCancel    = 9
)

// IAsyncOperation<T>/IAsyncAction slot of GetResults.
const asyncGetResults = 8

// Await waits for a WinRT async operation (IAsyncOperation<T> or
// IAsyncAction) and returns its result pointer (nil for actions). It polls
// IAsyncInfo and pumps messages, so it works on STA and MTA threads alike.
// When ctx is done the operation is canceled.
func Await(ctx context.Context, op *Unknown, hasResult bool) (*Unknown, error) {
	info, err := op.QueryInterface(&IIDIAsyncInfo)
	if err != nil {
		return nil, err
	}
	defer info.Release()

	wait := uint32(1)
	for {
		var status int32
		if err := info.CallHR("IAsyncInfo.get_Status", asyncInfoStatus, uintptr(unsafe.Pointer(&status))); err != nil {
			return nil, err
		}
		switch status {
		case AsyncCompleted:
			if !hasResult {
				return nil, op.CallHR("IAsyncAction.GetResults", asyncGetResults)
			}
			var res *Unknown
			if err := op.CallHR("IAsyncOperation.GetResults", asyncGetResults, uintptr(unsafe.Pointer(&res))); err != nil {
				return nil, err
			}
			if res == nil {
				return nil, ErrNilPointer
			}
			return res, nil
		case AsyncCanceled:
			return nil, &Error{HR: E_ABORT, Op: "async operation"}
		case AsyncError:
			var hr int32
			info.Call(asyncInfoErrorCode, uintptr(unsafe.Pointer(&hr)))
			if err := HR("async operation", uintptr(uint32(hr))); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("com: async operation failed without error code")
		}
		select {
		case <-ctx.Done():
			info.Call(asyncInfoCancel)
			return nil, ctx.Err()
		default:
		}
		Pump(wait)
		if wait < 16 {
			wait *= 2
		}
	}
}
