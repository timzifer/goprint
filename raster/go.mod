module github.com/timzifer/goprint/raster

go 1.26.4

require (
	github.com/timzifer/cera v0.6.1
	github.com/timzifer/goprint v0.5.0
)

require (
	github.com/ajroetker/go-highway v0.0.12 // indirect
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-images/jpeg v0.2.0 // indirect
	github.com/go-images/jpeg2000 v0.13.2 // indirect
	github.com/go-opentype/fonts v0.10.0 // indirect
	github.com/go-opentype/opentype v0.13.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/tannevaled/gobig2 v0.2.0 // indirect
	github.com/timzifer/stilus v0.9.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)

// The raster module is developed in the goprint repository and released
// together with it.
replace github.com/timzifer/goprint => ../
