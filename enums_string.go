package goprint

func (o Orientation) String() string {
	switch o {
	case OrientationDefault:
		return "default"
	case Portrait:
		return "portrait"
	case Landscape:
		return "landscape"
	case ReversePortrait:
		return "reverse-portrait"
	case ReverseLandscape:
		return "reverse-landscape"
	}
	return "unknown"
}

func (d Duplex) String() string {
	switch d {
	case DuplexDefault:
		return "default"
	case DuplexNone:
		return "one-sided"
	case DuplexLongEdge:
		return "two-sided-long-edge"
	case DuplexShortEdge:
		return "two-sided-short-edge"
	}
	return "unknown"
}

func (c ColorMode) String() string {
	switch c {
	case ColorAuto:
		return "auto"
	case Color:
		return "color"
	case Monochrome:
		return "monochrome"
	}
	return "unknown"
}

func (q Quality) String() string {
	switch q {
	case QualityDefault:
		return "default"
	case QualityDraft:
		return "draft"
	case QualityNormal:
		return "normal"
	case QualityHigh:
		return "high"
	}
	return "unknown"
}

func (s Scaling) String() string {
	switch s {
	case ScalingDefault:
		return "default"
	case ScalingFit:
		return "fit"
	case ScalingFill:
		return "fill"
	case ScalingNone:
		return "none"
	}
	return "unknown"
}

func (s DialogStyle) String() string {
	switch s {
	case StyleAuto:
		return "auto"
	case StyleModern:
		return "modern"
	case StyleClassic:
		return "classic"
	}
	return "unknown"
}
