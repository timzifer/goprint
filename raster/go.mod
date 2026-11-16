module github.com/timzifer/goprint/raster

go 1.27.1

require (
	github.com/timzifer/cera v0.13.0
	github.com/timzifer/goprint v0.6.0
)

require (
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-images/jpeg v0.3.0 // indirect
	github.com/go-images/jpeg2000 v0.14.0 // indirect
	github.com/go-opentype/fonts v0.12.0 // indirect
	github.com/go-opentype/opentype v0.15.0 // indirect
	github.com/go-simd/highway v0.0.13 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/tannevaled/gobig2 v0.2.0 // indirect
	github.com/timzifer/stilus v0.20.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

// The raster module is developed in the goprint repository and released
// together with it.
replace github.com/timzifer/goprint => ../
