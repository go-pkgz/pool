module examples/direct_chain

go 1.24.0

toolchain go1.24.6

require github.com/go-pkgz/pool v0.7.0

require golang.org/x/sync v0.19.0 // indirect

replace github.com/go-pkgz/pool => ../..
