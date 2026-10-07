package goprint

import (
	"fmt"
	"testing"
)

func TestEnumStrings(t *testing.T) {
	for _, v := range []fmt.Stringer{
		OrientationDefault, Portrait, Landscape, ReversePortrait, ReverseLandscape,
		DuplexDefault, DuplexNone, DuplexLongEdge, DuplexShortEdge,
		ColorAuto, Color, Monochrome,
		QualityDefault, QualityDraft, QualityNormal, QualityHigh,
		ScalingDefault, ScalingFit, ScalingFill, ScalingNone,
		StyleAuto, StyleModern, StyleClassic,
		JobPending, JobProcessing, JobCompleted, JobCanceled, JobAborted,
	} {
		if s := v.String(); s == "" || s == "unknown" {
			t.Errorf("%T(%v) has no name", v, v)
		}
	}
	if Orientation(99).String() != "unknown" {
		t.Error("out-of-range value")
	}
}
