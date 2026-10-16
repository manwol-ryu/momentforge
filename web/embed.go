// Package web embeds the built browser app. `npm run build` (make build)
// writes it to web/dist; this package has no other code.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built app rooted at dist/.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // only fails for an invalid literal path
	}
	return sub
}
