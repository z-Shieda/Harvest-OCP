module inventario

go 1.24

// O pacote golang.org/x/sys é obtido pelo espelho oficial no GitHub.
replace golang.org/x/sys => github.com/golang/sys v0.30.0

require (
	github.com/ncruces/go-sqlite3 v0.22.0
	github.com/tetratelabs/wazero v1.8.2
)

require (
	github.com/ncruces/julianday v1.0.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
)
