package fyneprint

import (
	"context"
	"errors"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"

	"github.com/timzifer/goprint"
)

func stubDialog(t *testing.T, f func(context.Context, goprint.Document, goprint.DialogOptions) (*goprint.Job, goprint.Settings, error)) {
	t.Helper()
	saved := dialogFunc
	dialogFunc = f
	t.Cleanup(func() { dialogFunc = saved })
}

func TestOwnerTestWindow(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	if h := Owner(w); h != 0 {
		t.Errorf("Owner(test window) = %#x, want 0", h)
	}
}

func TestDialogKeepsOwner(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	var got goprint.DialogOptions
	stubDialog(t, func(_ context.Context, _ goprint.Document, o goprint.DialogOptions) (*goprint.Job, goprint.Settings, error) {
		got = o
		return nil, goprint.Settings{Printer: "P"}, nil
	})
	_, s, err := Dialog(context.Background(), w, goprint.Document{}, goprint.DialogOptions{Style: goprint.StyleModern})
	if err != nil || s.Printer != "P" {
		t.Fatalf("Dialog = %+v, %v", s, err)
	}
	if got.Owner != 0 || got.Style != goprint.StyleModern {
		t.Errorf("options passed on = %+v", got)
	}
}

func TestShowDialogCallsBack(t *testing.T) {
	test.NewTempApp(t)
	w := test.NewTempWindow(t, nil)
	want := errors.New("boom")
	stubDialog(t, func(context.Context, goprint.Document, goprint.DialogOptions) (*goprint.Job, goprint.Settings, error) {
		return nil, goprint.Settings{Copies: 3}, want
	})
	type result struct {
		s   goprint.Settings
		err error
	}
	ch := make(chan result, 1)
	ShowDialog(w, goprint.Document{}, goprint.DialogOptions{}, func(_ *goprint.Job, s goprint.Settings, err error) {
		ch <- result{s, err}
	})
	select {
	case r := <-ch:
		if r.s.Copies != 3 || !errors.Is(r.err, want) {
			t.Errorf("callback got %+v, %v", r.s, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("done was not called")
	}
}
